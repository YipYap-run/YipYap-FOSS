package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type PublicHandler struct {
	store store.Store
}

func NewPublicHandler(s store.Store) *PublicHandler {
	return &PublicHandler{store: s}
}

// MonitorStatus returns a single monitor's status from the public status page.
func (h *PublicHandler) MonitorStatus(w http.ResponseWriter, r *http.Request) {
	orgSlug := chi.URLParam(r, "orgSlug")
	monitorID := chi.URLParam(r, "monitorID")

	org, err := h.store.Orgs().GetBySlug(r.Context(), orgSlug)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "org not found")
		return
	}

	m, err := h.store.Monitors().GetByID(r.Context(), monitorID)
	if err != nil || m.OrgID != org.ID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	result := map[string]interface{}{
		"id":     m.ID,
		"name":   m.Name,
		"status": "unknown",
	}

	latest, err := h.store.Checks().GetLatest(r.Context(), m.ID)
	if err == nil {
		result["status"] = string(latest.Status)
		result["latency_ms"] = latest.LatencyMS
		result["checked_at"] = latest.CheckedAt
	}

	jsonResponse(w, http.StatusOK, result)
}

// Maintenance returns active public maintenance windows for an org.
// Returns 200 with an empty array when the org does not exist to prevent
// org existence enumeration.
func (h *PublicHandler) Maintenance(w http.ResponseWriter, r *http.Request) {
	orgSlug := chi.URLParam(r, "orgSlug")
	org, err := h.store.Orgs().GetBySlug(r.Context(), orgSlug)
	if err != nil {
		// Return an empty list rather than 404 to avoid leaking whether
		// the org exists.
		jsonResponse(w, http.StatusOK, []*domain.MaintenanceWindow{})
		return
	}

	windows, err := h.store.MaintenanceWindows().ListPublicByOrg(r.Context(), org.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list maintenance windows")
		return
	}
	if windows == nil {
		windows = []*domain.MaintenanceWindow{}
	}
	jsonResponse(w, http.StatusOK, windows)
}
