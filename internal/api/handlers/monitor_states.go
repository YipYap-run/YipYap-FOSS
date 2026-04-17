package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// MonitorStateHandler handles custom monitor state and match rule endpoints.
type MonitorStateHandler struct {
	store store.Store
}

// NewMonitorStateHandler creates a new MonitorStateHandler.
func NewMonitorStateHandler(s store.Store) *MonitorStateHandler {
	return &MonitorStateHandler{store: s}
}

// ---------------------------------------------------------------------------
// Monitor States
// ---------------------------------------------------------------------------

func (h *MonitorStateHandler) stateStore() store.MonitorStateStore {
	if p, ok := h.store.(store.MonitorStateProvider); ok {
		return p.MonitorStates()
	}
	return nil
}

func (h *MonitorStateHandler) ruleStore() store.MonitorMatchRuleStore {
	if p, ok := h.store.(store.MonitorMatchRuleProvider); ok {
		return p.MonitorMatchRules()
	}
	return nil
}

// List returns all monitor states for the authenticated org.
func (h *MonitorStateHandler) List(w http.ResponseWriter, r *http.Request) {
	ss := h.stateStore()
	if ss == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor states not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	states, err := ss.ListByOrg(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list monitor states")
		return
	}
	jsonResponse(w, http.StatusOK, states)
}

// Create creates a new custom monitor state.
func (h *MonitorStateHandler) Create(w http.ResponseWriter, r *http.Request) {
	ss := h.stateStore()
	if ss == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor states not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())

	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		HealthClass string `json:"health_class"`
		Severity    string `json:"severity"`
		Color       string `json:"color"`
		Position    int    `json:"position"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Slug == "" {
		errorResponse(w, http.StatusBadRequest, "name and slug are required")
		return
	}
	if !isValidHealthClass(req.HealthClass) {
		errorResponse(w, http.StatusBadRequest, "health_class must be healthy, degraded, or unhealthy")
		return
	}
	if !isValidSeverity(req.Severity) {
		errorResponse(w, http.StatusBadRequest, "severity must be critical, warning, or info")
		return
	}
	if !isValidHexColor(req.Color) {
		errorResponse(w, http.StatusBadRequest, "color must be a valid hex color (e.g. #ff0000)")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	st := &domain.MonitorState{
		ID:          uuid.New().String(),
		OrgID:       claims.OrgID,
		Name:        req.Name,
		Slug:        req.Slug,
		HealthClass: req.HealthClass,
		Severity:    req.Severity,
		Color:       req.Color,
		Position:    req.Position,
		IsBuiltin:   false,
		CreatedAt:   now,
	}
	if err := ss.Create(r.Context(), st); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create monitor state")
		return
	}
	jsonResponse(w, http.StatusCreated, st)
}

// Update modifies an existing monitor state.
func (h *MonitorStateHandler) Update(w http.ResponseWriter, r *http.Request) {
	ss := h.stateStore()
	if ss == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor states not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := ss.GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor state not found")
		return
	}
	if existing.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor state not found")
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		HealthClass *string `json:"health_class"`
		Severity    *string `json:"severity"`
		Color       *string `json:"color"`
		Position    *int    `json:"position"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Built-in states only allow cosmetic changes (color, position).
	if existing.IsBuiltin {
		if req.Name != nil || req.Severity != nil || req.Slug != nil || req.HealthClass != nil {
			errorResponse(w, http.StatusBadRequest, "built-in states only support color and position changes")
			return
		}
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.HealthClass != nil {
		if !isValidHealthClass(*req.HealthClass) {
			errorResponse(w, http.StatusBadRequest, "health_class must be healthy, degraded, or unhealthy")
			return
		}
		existing.HealthClass = *req.HealthClass
	}
	if req.Severity != nil {
		if !isValidSeverity(*req.Severity) {
			errorResponse(w, http.StatusBadRequest, "severity must be critical, warning, or info")
			return
		}
		existing.Severity = *req.Severity
	}
	if req.Color != nil {
		if !isValidHexColor(*req.Color) {
			errorResponse(w, http.StatusBadRequest, "color must be a valid hex color (e.g. #ff0000)")
			return
		}
		existing.Color = *req.Color
	}
	if req.Position != nil {
		existing.Position = *req.Position
	}

	if err := ss.Update(r.Context(), existing); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update monitor state")
		return
	}
	jsonResponse(w, http.StatusOK, existing)
}

// Delete removes a custom monitor state.
func (h *MonitorStateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ss := h.stateStore()
	if ss == nil {
		errorResponse(w, http.StatusNotImplemented, "monitor states not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := ss.GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor state not found")
		return
	}
	if existing.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor state not found")
		return
	}
	if existing.IsBuiltin {
		errorResponse(w, http.StatusBadRequest, "cannot delete built-in state")
		return
	}

	if err := ss.Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to delete monitor state")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Match Rules
// ---------------------------------------------------------------------------

// ListRules returns all match rules for a monitor.
func (h *MonitorStateHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	rs := h.ruleStore()
	if rs == nil {
		errorResponse(w, http.StatusNotImplemented, "match rules not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	monitorID := chi.URLParam(r, "id")

	// Verify monitor belongs to this org.
	mon, err := h.store.Monitors().GetByID(r.Context(), monitorID)
	if err != nil || mon.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	rules, err := rs.ListByMonitor(r.Context(), monitorID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list match rules")
		return
	}
	jsonResponse(w, http.StatusOK, rules)
}

// ReplaceRules replaces all match rules for a monitor.
func (h *MonitorStateHandler) ReplaceRules(w http.ResponseWriter, r *http.Request) {
	rs := h.ruleStore()
	ss := h.stateStore()
	if rs == nil || ss == nil {
		errorResponse(w, http.StatusNotImplemented, "match rules not supported")
		return
	}
	claims := middleware.GetClaims(r.Context())
	monitorID := chi.URLParam(r, "id")

	// Verify monitor belongs to this org.
	mon, err := h.store.Monitors().GetByID(r.Context(), monitorID)
	if err != nil || mon.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	var rules []*domain.MonitorMatchRule
	if err := decodeBody(r, &rules); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate state_ids exist and belong to this org.
	states, err := ss.ListByOrg(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to validate states")
		return
	}
	validStates := make(map[string]bool, len(states))
	for _, s := range states {
		validStates[s.ID] = true
	}

	for i, rule := range rules {
		if rule.StateID == "" {
			errorResponse(w, http.StatusBadRequest, "state_id is required for each rule")
			return
		}
		if !validStates[rule.StateID] {
			errorResponse(w, http.StatusBadRequest, "invalid state_id: "+rule.StateID)
			return
		}
		if rule.BodyMatchMode == "regex" && rule.BodyMatch != "" {
			if len(rule.BodyMatch) > 512 {
				errorResponse(w, http.StatusBadRequest, fmt.Sprintf("rules[%d]: regex pattern too long (max 512 chars)", i))
				return
			}
			if _, err := regexp.Compile(rule.BodyMatch); err != nil {
				errorResponse(w, http.StatusBadRequest, fmt.Sprintf("rules[%d]: invalid regex: %v", i, err))
				return
			}
		}
		rule.ID = uuid.New().String()
		rule.MonitorID = monitorID
		rule.Position = i
	}

	if err := rs.ReplaceForMonitor(r.Context(), monitorID, rules); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to replace match rules")
		return
	}

	// Return the saved rules with joins.
	saved, err := rs.ListByMonitor(r.Context(), monitorID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read saved rules")
		return
	}
	jsonResponse(w, http.StatusOK, saved)
}

// ---------------------------------------------------------------------------
// validation helpers
// ---------------------------------------------------------------------------

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func isValidHealthClass(s string) bool {
	return s == "healthy" || s == "degraded" || s == "unhealthy"
}

func isValidSeverity(s string) bool {
	return s == "critical" || s == "warning" || s == "info"
}

func isValidHexColor(s string) bool {
	return hexColorRe.MatchString(s)
}
