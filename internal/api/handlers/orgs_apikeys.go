package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// DefaultAPIKeyMaxTTL bounds how far in the future an API key's expires_at
// may be set when the operator has not configured YIPYAP_API_KEY_MAX_TTL. It
// matches the UI's latest pickable date.
const DefaultAPIKeyMaxTTL = APIKeyMaxExpiryDays * 24 * time.Hour

const maxAPIKeyNameLen = 100

const (
	// APIKeyDefaultExpiryDays is used when a create request omits expires_at.
	APIKeyDefaultExpiryDays = 90
	// APIKeyMaxExpiryDays caps an explicit expiry date. Keys that should
	// never expire say so with "never" instead of a far-off date.
	APIKeyMaxExpiryDays = 730
	// apiKeyMinLifetime rejects expiry dates that are in the past or so
	// close that the key would be useless.
	apiKeyMinLifetime = time.Hour
	apiKeyMaxNameLen  = 100
)

var apiKeyMaxTTL atomic.Int64

func init() { apiKeyMaxTTL.Store(int64(DefaultAPIKeyMaxTTL)) }

// SetAPIKeyMaxTTL sets the longest allowed lifetime for a new API key that
// has an expiry. Zero or negative restores the default.
func SetAPIKeyMaxTTL(d time.Duration) {
	if d <= 0 {
		d = DefaultAPIKeyMaxTTL
	}
	apiKeyMaxTTL.Store(int64(d))
}

func currentAPIKeyMaxTTL() time.Duration { return time.Duration(apiKeyMaxTTL.Load()) }

// apiKeyView is an API key as the settings UI lists it: the stored key plus
// who created it. Keys belong to the org, so the creator may have left.
type apiKeyView struct {
	*domain.APIKey
	CreatorEmail   string `json:"creator_email,omitempty"`
	CreatorName    string `json:"creator_name,omitempty"`
	CreatorRemoved bool   `json:"creator_removed"`
	Expired        bool   `json:"expired"`
	// Unrestricted marks a key created before scopes were required. It
	// has no scopes and is treated as full-access.
	Unrestricted bool `json:"unrestricted"`
}

func apiKeyViews(ctx context.Context, s store.Store, orgID string, keys []*domain.APIKey) []apiKeyView {
	now := time.Now()
	users := map[string]*domain.User{}
	out := make([]apiKeyView, 0, len(keys))
	for _, k := range keys {
		v := apiKeyView{
			APIKey:       k,
			Expired:      middleware.APIKeyExpired(k.ExpiresAt, now),
			Unrestricted: len(k.Scopes) == 0,
		}
		if k.Scopes == nil {
			k.Scopes = []string{}
		}
		if k.CreatedBy != "" {
			u, seen := users[k.CreatedBy]
			if !seen {
				if got, err := s.Users().GetByID(ctx, k.CreatedBy); err == nil && got.OrgID == orgID && got.DeletedAt == nil {
					u = got
				}
				users[k.CreatedBy] = u
			}
			if u != nil {
				v.CreatorEmail = u.Email
				v.CreatorName = u.Name
			} else {
				v.CreatorRemoved = true
			}
		}
		out = append(out, v)
	}
	return out
}

func (h *OrgHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	keys, err := h.store.APIKeys().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list api keys")
		return
	}
	jsonResponse(w, http.StatusOK, apiKeyViews(r.Context(), h.store, claims.OrgID, keys))
}

// ListAPIKeyScopes returns the scope vocabulary CreateAPIKey accepts and
// RequireScope enforces, the presets the create form offers, and the expiry
// bounds.
func (h *OrgHandler) ListAPIKeyScopes(w http.ResponseWriter, r *http.Request) {
	byKey := map[string]middleware.APIKeyScopeResource{}
	for _, res := range middleware.APIKeyScopeResources() {
		byKey[res.Key] = res
	}
	resources := make([]map[string]interface{}, 0, len(middleware.ScopeVocabulary()))
	for _, res := range middleware.ScopeVocabulary() {
		actions := []string{middleware.ScopeActionRead, middleware.ScopeActionWrite}
		description := res.Label
		if rich, ok := byKey[res.Resource]; ok {
			actions = rich.Actions
			description = rich.Description
		}
		resources = append(resources, map[string]interface{}{
			"resource":    res.Resource,
			"key":         res.Resource,
			"label":       res.Label,
			"description": description,
			"read":        res.Read,
			"write":       res.Write,
			"read_only":   res.ReadOnly,
			"actions":     actions,
		})
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"presets":             middleware.APIKeyScopePresets(),
		"resources":           resources,
		"scopes":              middleware.AllowedScopes(),
		"write_implies_read":  true,
		"default_expiry_days": APIKeyDefaultExpiryDays,
		"max_expiry_days":     int(currentAPIKeyMaxTTL() / (24 * time.Hour)),
		"notes": map[string]string{
			"write_implies_read": "A <resource>:write scope also grants <resource>:read.",
			"empty_scopes":       "A key created with no scopes has full access to every scoped resource.",
			"never_allowed":      "API keys cannot manage API keys, SSO, billing, members or account security, whatever their scopes.",
		},
	})
}

// normalizeNewAPIKeyScopes validates scopes for a new key against the
// vocabulary this build enforces. Presets are kept as presets; a bare
// resource name is the legacy stored form and cannot be created; a read
// scope covered by the same resource's write scope is dropped.
func normalizeNewAPIKeyScopes(in []string) ([]string, error) {
	known := map[string]bool{}
	for _, s := range middleware.AllowedScopes() {
		known[s] = true
	}
	presets := map[string]bool{}
	grants := map[string]string{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		switch s {
		case middleware.ScopePresetReadOnly, middleware.ScopePresetFullAccess:
			presets[s] = true
			continue
		}
		res, action, ok := strings.Cut(s, ":")
		if !ok || !known[s] || (action != middleware.ScopeRead && action != middleware.ScopeWrite) {
			return nil, fmt.Errorf("unknown scope %q", raw)
		}
		if grants[res] != middleware.ScopeWrite {
			grants[res] = action
		}
	}
	if len(presets) == 0 && len(grants) == 0 {
		return nil, fmt.Errorf("choose at least one scope")
	}
	if presets[middleware.ScopePresetFullAccess] {
		return []string{middleware.ScopePresetFullAccess}, nil
	}
	out := []string{}
	if presets[middleware.ScopePresetReadOnly] {
		out = append(out, middleware.ScopePresetReadOnly)
	}
	for _, r := range middleware.ScopeVocabulary() {
		action, ok := grants[r.Resource]
		if !ok {
			continue
		}
		if action == middleware.ScopeRead && presets[middleware.ScopePresetReadOnly] {
			continue
		}
		out = append(out, r.Resource+":"+action)
	}
	return out, nil
}

// unknownAPIKeyScopes lists the requested entries the vocabulary does not
// know, for the client's error detail.
func unknownAPIKeyScopes(in []string) []string {
	known := map[string]bool{}
	for _, s := range middleware.AllowedScopes() {
		known[s] = true
	}
	out := []string{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == middleware.ScopePresetReadOnly || s == middleware.ScopePresetFullAccess || known[s] {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// parseAPIKeyExpiry reads the expires_at field of a create request. Omitted
// means the default lifetime; null or "never" means no expiry; otherwise it
// must be an RFC 3339 time at least an hour out and within the maximum.
func parseAPIKeyExpiry(raw json.RawMessage, now time.Time) (*time.Time, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		t := now.AddDate(0, 0, APIKeyDefaultExpiryDays)
		return &t, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf(`expires_at must be an RFC 3339 time, "never" or null`)
	}
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "never") {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf(`expires_at must be an RFC 3339 time, "never" or null`)
	}
	t = t.UTC().Truncate(time.Second)
	if t.Before(now.Add(apiKeyMinLifetime)) {
		return nil, fmt.Errorf("expires_at must be at least one hour in the future")
	}
	maxTTL := currentAPIKeyMaxTTL()
	if t.Sub(now) > maxTTL {
		return nil, fmt.Errorf("expires_at must be within %s of now", formatDays(maxTTL))
	}
	return &t, nil
}

func (h *OrgHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	var req struct {
		Name      string          `json:"name"`
		Scopes    []string        `json:"scopes"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		errorResponse(w, http.StatusBadRequest, "name is required")
		return
	}
	if utf8.RuneCountInString(req.Name) > apiKeyMaxNameLen {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf("name can be at most %d characters", apiKeyMaxNameLen))
		return
	}
	if len(req.Name) > maxAPIKeyNameLen {
		errorResponse(w, http.StatusBadRequest, "name must be 100 characters or fewer")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	expiresAt, err := parseAPIKeyExpiry(req.ExpiresAt, now)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	scopes, err := normalizeNewAPIKeyScopes(req.Scopes)
	if err != nil {
		allowed := middleware.AllowedScopes()
		jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error":          err.Error() + ". Allowed: " + strings.Join(allowed, ", "),
			"code":           "invalid_scope",
			"unknown_scopes": unknownAPIKeyScopes(req.Scopes),
			"allowed_scopes": allowed,
		})
		return
	}

	if h.hasher == nil {
		errorResponse(w, http.StatusInternalServerError, "api key generation not configured")
		return
	}
	plaintext, hash, prefix, genErr := h.hasher.GenerateAPIKey()
	if genErr != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	apiKey := &domain.APIKey{
		ID:        uuid.New().String(),
		OrgID:     claims.OrgID,
		Name:      req.Name,
		KeyHash:   hash,
		Prefix:    prefix,
		Scopes:    scopes,
		CreatedBy: claims.UserID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}
	if apiKey.Scopes == nil {
		apiKey.Scopes = []string{}
	}

	if err := h.store.APIKeys().Create(r.Context(), apiKey); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]interface{}{
		"key":        plaintext,
		"id":         apiKey.ID,
		"name":       apiKey.Name,
		"prefix":     apiKey.Prefix,
		"scopes":     apiKey.Scopes,
		"created_by": apiKey.CreatedBy,
		"expires_at": apiKey.ExpiresAt,
		"created":    apiKey.CreatedAt,
	})
}

func (h *OrgHandler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key, err := h.store.APIKeys().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if key.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := h.store.APIKeys().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func formatDays(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	if days == 1 {
		return "1 day"
	}
	return strconv.Itoa(days) + " days"
}
