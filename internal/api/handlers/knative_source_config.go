package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// sourceConfigSettingKey is the OrgSettings key under which per-org
// knative-source hints live. The value is the JSON-encoded form of
// sourceConfigResponse. See KnativeSourceConfigHandler.Get for the merge
// rules.
const sourceConfigSettingKey = "knative.source_config"

// Ship-level defaults. Applied when the OrgSettings row is missing, or a
// particular field in the stored blob is zero-valued (int zero, empty
// string). These match the behaviour documented in Phase 3 planning:
// a conservative 5s poll and a 5-minute refresh are sensible for most orgs.
const (
	defaultKnativeRecommendedMode    = "poll"
	defaultKnativePollIntervalSecs   = 5
	defaultKnativeRefreshAfterSecs   = 300
)

// KnativeSourceConfigHandler serves the per-org server-hint endpoint used by
// the yipyap-knative-source ContainerSource binary. The binary fetches this
// on startup and periodically thereafter to decide whether to poll or
// stream, and at what cadence.
type KnativeSourceConfigHandler struct {
	store store.Store
}

// NewKnativeSourceConfigHandler returns a new KnativeSourceConfigHandler.
func NewKnativeSourceConfigHandler(s store.Store) *KnativeSourceConfigHandler {
	return &KnativeSourceConfigHandler{store: s}
}

// sourceConfigResponse is the on-the-wire JSON shape returned to the
// ContainerSource binary. The same shape is used for the OrgSettings
// override blob under sourceConfigSettingKey.
type sourceConfigResponse struct {
	RecommendedMode     string `json:"recommended_mode"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	RefreshAfterSeconds int    `json:"refresh_after_seconds"`
}

// Get handles GET /api/v1/knative/source-config.
//
// Auth is provided by the outer authMiddleware, either a JWT (for
// SaaS/admin testing) or an API key with scope set by OrgSettings. We
// resolve the orgID off the claims and then merge any per-org override
// over the ship-level defaults. Malformed OrgSettings rows are treated as
// absent rather than surfacing a 5xx: a fat-fingered JSON blob in a
// console shouldn't tip over every ContainerSource in an org.
func (h *KnativeSourceConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		// Defence in depth, authMiddleware should have already rejected
		// this path. Respond with 401 rather than panicking so misrouted
		// requests return a well-formed error.
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	resp := sourceConfigResponse{
		RecommendedMode:     defaultKnativeRecommendedMode,
		PollIntervalSeconds: defaultKnativePollIntervalSecs,
		RefreshAfterSeconds: defaultKnativeRefreshAfterSecs,
	}

	// Mirror the Task-2.4 pattern: OrgSettings is exposed via an optional
	// interface assertion so test doubles without settings support still
	// work. A missing row, empty value, or malformed JSON all degrade to
	// the ship-level defaults.
	if provider, ok := h.store.(store.OrgSettingsProvider); ok {
		settings := provider.OrgSettings()
		if settings != nil {
			raw, err := settings.Get(r.Context(), claims.OrgID, sourceConfigSettingKey)
			if err == nil {
				applySourceConfigOverride(&resp, raw)
			}
			// On error we fall through, missing rows, storage hiccups,
			// or malformed JSON all map to "use defaults". Returning 500
			// here would couple ContainerSource uptime to operator JSON
			// hygiene.
		}
	}

	// Validate recommended_mode after override; anything outside
	// {"poll","stream"} collapses to the safe default. We do this last
	// so a partial override of the two interval fields still lands even
	// when the mode field is garbage.
	if resp.RecommendedMode != "poll" && resp.RecommendedMode != "stream" {
		resp.RecommendedMode = defaultKnativeRecommendedMode
	}

	jsonResponse(w, http.StatusOK, resp)
}

// applySourceConfigOverride merges a raw OrgSettings blob into resp. Only
// non-zero fields in the blob override the caller-supplied defaults, so
// operators can specify just the dimensions they care about. Malformed
// JSON is silently ignored, the caller treats this as "row absent".
func applySourceConfigOverride(resp *sourceConfigResponse, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var override sourceConfigResponse
	if err := json.Unmarshal([]byte(raw), &override); err != nil {
		return
	}
	if override.RecommendedMode != "" {
		resp.RecommendedMode = override.RecommendedMode
	}
	if override.PollIntervalSeconds > 0 {
		resp.PollIntervalSeconds = override.PollIntervalSeconds
	}
	if override.RefreshAfterSeconds > 0 {
		resp.RefreshAfterSeconds = override.RefreshAfterSeconds
	}
}
