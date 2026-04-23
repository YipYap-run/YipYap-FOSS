package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// MonitorGroupHandler handles monitor group CRUD endpoints.
type MonitorGroupHandler struct {
	store store.Store
}

// NewMonitorGroupHandler creates a new MonitorGroupHandler.
func NewMonitorGroupHandler(s store.Store) *MonitorGroupHandler {
	return &MonitorGroupHandler{store: s}
}

func (h *MonitorGroupHandler) groupStore() store.MonitorGroupStore {
	if p, ok := h.store.(store.MonitorGroupProvider); ok {
		return p.MonitorGroups()
	}
	return nil
}

// List returns all monitor groups for the authenticated org.
func (h *MonitorGroupHandler) List(w http.ResponseWriter, r *http.Request) {
	gs := h.groupStore()
	if gs == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor groups not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	groups, err := gs.ListByOrg(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list monitor groups")
		return
	}
	jsonResponse(w, http.StatusOK, groups)
}

// monitorGroupDetailResponse wraps a MonitorGroup with its monitors.
type monitorGroupDetailResponse struct {
	*domain.MonitorGroup
	Monitors []*domain.Monitor `json:"monitors"`
}

// Get returns a single monitor group with its monitors.
func (h *MonitorGroupHandler) Get(w http.ResponseWriter, r *http.Request) {
	gs := h.groupStore()
	if gs == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor groups not supported")
		return
	}
	id := chi.URLParam(r, "id")
	group, err := gs.GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor group not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if group.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor group not found")
		return
	}

	// Fetch monitors in this group.
	allMonitors, err := h.store.Monitors().ListByOrg(r.Context(), claims.OrgID, store.MonitorFilter{
		ListParams: store.ListParams{Limit: 500},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	var groupMonitors []*domain.Monitor
	for _, m := range allMonitors {
		if m.GroupID == id {
			groupMonitors = append(groupMonitors, m)
		}
	}

	jsonResponse(w, http.StatusOK, monitorGroupDetailResponse{
		MonitorGroup: group,
		Monitors:     groupMonitors,
	})
}

// Create creates a new monitor group.
func (h *MonitorGroupHandler) Create(w http.ResponseWriter, r *http.Request) {
	gs := h.groupStore()
	if gs == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor groups not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		errorResponse(w, http.StatusBadRequest, "name is required")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	g := &domain.MonitorGroup{
		ID:          uuid.New().String(),
		OrgID:       claims.OrgID,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := gs.Create(r.Context(), g); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create monitor group")
		return
	}
	jsonResponse(w, http.StatusCreated, g)
}

// Update patches a monitor group's name and/or description.
func (h *MonitorGroupHandler) Update(w http.ResponseWriter, r *http.Request) {
	gs := h.groupStore()
	if gs == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor groups not supported")
		return
	}
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	group, err := gs.GetByID(r.Context(), id)
	if err != nil || group.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor group not found")
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		group.Name = *req.Name
	}
	if req.Description != nil {
		group.Description = *req.Description
	}
	group.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := gs.Update(r.Context(), group); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update monitor group")
		return
	}
	jsonResponse(w, http.StatusOK, group)
}

// Delete removes a monitor group. Monitors in the group are ungrouped (group_id set to NULL).
func (h *MonitorGroupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	gs := h.groupStore()
	if gs == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor groups not supported")
		return
	}
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	group, err := gs.GetByID(r.Context(), id)
	if err != nil || group.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor group not found")
		return
	}

	// The FK ON DELETE SET NULL handles ungrouping monitors automatically.
	if err := gs.Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to delete monitor group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
