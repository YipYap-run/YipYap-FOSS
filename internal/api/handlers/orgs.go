package handlers

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// allowedOrgSettingKeys are the org setting keys the frontend may read/write.
// All of them are booleans stored as "true"; false is stored as no row.
var allowedOrgSettingKeys = map[string]bool{
	"mute_new_monitors":             true,
	"incident_auto_resolve":         true,
	OrgSettingAllowResolveWhileDown: true,
}

const (
	// maxMFAGraceDays bounds the org MFA grace period (org-8).
	maxMFAGraceDays = 30
	// defaultMFAGraceDays is what enforcement uses when no grace period is
	// set. It matches the auth middleware's default.
	defaultMFAGraceDays = 7
	orgSlugMaxLen       = 63
)

// reservedOrgSlugs would shadow server routes in the public status page URL
// /{slug}/status/{page}.
var reservedOrgSlugs = map[string]bool{
	"api": true, "internal": true, "assets": true, "static": true,
}

var orgSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validateOrgSlug normalizes case and surrounding space, then checks the
// slug as given. It returns the slug or a client-facing error message.
func validateOrgSlug(raw string) (string, string) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return "", "slug cannot be empty"
	case len(s) < 2 || len(s) > orgSlugMaxLen:
		return "", "slug must be 2 to 63 characters"
	case !orgSlugRe.MatchString(s):
		return "", "slug may contain only lowercase letters, numbers and single hyphens, and cannot start or end with a hyphen"
	case reservedOrgSlugs[s]:
		return "", "slug is reserved"
	}
	return s, ""
}

type OrgHandler struct {
	store  store.Store
	hasher *auth.APIKeyHasher
}

func NewOrgHandler(s store.Store) *OrgHandler {
	return &OrgHandler{store: s}
}

// NewOrgHandlerWithHasher returns an OrgHandler that uses the given APIKeyHasher
// when generating API key hashes.
func NewOrgHandlerWithHasher(s store.Store, hasher *auth.APIKeyHasher) *OrgHandler {
	return &OrgHandler{store: s, hasher: hasher}
}

func (h *OrgHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "org not found")
		return
	}
	jsonResponse(w, http.StatusOK, org)
}

func (h *OrgHandler) Update(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "org not found")
		return
	}

	var req struct {
		Name          *string `json:"name"`
		Slug          *string `json:"slug"`
		OncallDisplay *string `json:"oncall_display"`
		MFARequired   *bool   `json:"mfa_required"`
		MFAGraceDays  *int    `json:"mfa_grace_days"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			errorResponse(w, http.StatusBadRequest, "organization name cannot be empty")
			return
		}
		// The slug is part of every public status page URL, so a rename
		// never touches it. Only an explicit slug changes it (org-7).
		org.Name = name
	}
	if req.Slug != nil {
		s, msg := validateOrgSlug(*req.Slug)
		if msg != "" {
			errorResponse(w, http.StatusBadRequest, msg)
			return
		}
		if s != org.Slug {
			other, err := h.store.Orgs().GetBySlug(r.Context(), s)
			if err == nil && other != nil && other.ID != org.ID {
				errorResponse(w, http.StatusConflict, "slug already taken")
				return
			}
		}
		org.Slug = s
	}
	if req.OncallDisplay != nil {
		v := *req.OncallDisplay
		if v == "name" || v == "email" {
			org.OncallDisplay = v
		}
	}
	if req.MFAGraceDays != nil && (*req.MFAGraceDays < 0 || *req.MFAGraceDays > maxMFAGraceDays) {
		errorResponse(w, http.StatusBadRequest, "mfa_grace_days must be between 0 and 30")
		return
	}
	// Requiring MFA, or tuning its grace period, is Pro+ (org-8). Turning
	// the requirement off is always allowed so a downgraded org is not stuck.
	enabling := req.MFARequired != nil && *req.MFARequired
	if (enabling || req.MFAGraceDays != nil) && !checkOrgMFAPlanGate(w, org) {
		return
	}
	mfaNewlyRequired := false
	if req.MFARequired != nil {
		mfaNewlyRequired = *req.MFARequired && !org.MFARequired
		org.MFARequired = *req.MFARequired
	}
	if req.MFAGraceDays != nil {
		org.MFAGraceDays = *req.MFAGraceDays
	}
	// Enforcement treats 0 as the default, so store the value it will use.
	if org.MFARequired && org.MFAGraceDays == 0 {
		org.MFAGraceDays = defaultMFAGraceDays
	}
	org.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if mfaNewlyRequired {
		// Deadlines left from an earlier requirement must not cut short the
		// grace period of this one. The org does not require MFA yet, so
		// clearing before the update is safe if the update fails.
		if err := h.store.Users().ClearMFAEnforcedAtByOrg(r.Context(), org.ID); err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to reset MFA grace periods")
			return
		}
	}
	if err := h.store.Orgs().Update(r.Context(), org); err != nil {
		if isUniqueViolation(err) {
			// Lost a race with another org claiming the same slug.
			errorResponse(w, http.StatusConflict, "slug already taken")
			return
		}
		errorResponse(w, http.StatusInternalServerError, "failed to update org")
		return
	}
	jsonResponse(w, http.StatusOK, org)
}

// GetSettings returns the allowed org-level KV settings.
func (h *OrgHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		jsonResponse(w, http.StatusOK, map[string]string{})
		return
	}
	claims := middleware.GetClaims(r.Context())
	all, err := sp.OrgSettings().GetAll(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	out := make(map[string]string)
	for k, v := range all {
		if allowedOrgSettingKeys[k] {
			out[k] = v
		}
	}
	jsonResponse(w, http.StatusOK, out)
}

// SetSettings writes allowed org-level KV settings.
func (h *OrgHandler) SetSettings(w http.ResponseWriter, r *http.Request) {
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		errorResponse(w, http.StatusInternalServerError, "settings not available")
		return
	}
	claims := middleware.GetClaims(r.Context())
	var body map[string]string
	if err := decodeBody(r, &body); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Validate every value before writing any, so a bad request changes
	// nothing.
	for k, v := range body {
		if !allowedOrgSettingKeys[k] {
			continue
		}
		switch strings.TrimSpace(v) {
		case "", "true", "false":
		default:
			errorResponse(w, http.StatusBadRequest, k+" must be true or false")
			return
		}
	}
	for k, v := range body {
		if !allowedOrgSettingKeys[k] {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || v == "false" {
			_ = sp.OrgSettings().Delete(r.Context(), claims.OrgID, k)
		} else {
			if err := sp.OrgSettings().Set(r.Context(), claims.OrgID, k, v); err != nil {
				errorResponse(w, http.StatusInternalServerError, "failed to save setting")
				return
			}
		}
	}
	// Return current settings.
	h.GetSettings(w, r)
}
