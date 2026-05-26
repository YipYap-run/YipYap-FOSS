package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// allowedOrgSettingKeys are the org setting keys the frontend may read/write.
var allowedOrgSettingKeys = map[string]bool{
	"mute_new_monitors":      true,
	"incident_auto_resolve":  true,
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
		if *req.Name == "" {
			errorResponse(w, http.StatusBadRequest, "organization name cannot be empty")
			return
		}
		org.Name = *req.Name
		// Auto-generate slug from name only if slug wasn't explicitly provided.
		if req.Slug == nil {
			org.Slug = slugify(*req.Name)
		}
	}
	if req.Slug != nil {
		s := slugify(*req.Slug)
		if s == "" {
			errorResponse(w, http.StatusBadRequest, "slug cannot be empty")
			return
		}
		org.Slug = s
	}
	if req.OncallDisplay != nil {
		v := *req.OncallDisplay
		if v == "name" || v == "email" {
			org.OncallDisplay = v
		}
	}
	if req.MFARequired != nil {
		org.MFARequired = *req.MFARequired
		if *req.MFARequired {
			if req.MFAGraceDays != nil {
				org.MFAGraceDays = *req.MFAGraceDays
			} else if org.MFAGraceDays == 0 {
				org.MFAGraceDays = 7
			}
		}
	} else if req.MFAGraceDays != nil {
		org.MFAGraceDays = *req.MFAGraceDays
	}
	org.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Orgs().Update(r.Context(), org); err != nil {
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
