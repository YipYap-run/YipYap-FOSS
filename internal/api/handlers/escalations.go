package handlers

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Length caps for the policy form fields, in characters.
const (
	maxPolicyName        = 200
	maxPolicyDescription = 2000
)

// cleanPolicyFields trims the name and description and checks their
// lengths. A failure comes back as a field-level 400 so the SPA can show it
// next to the input.
func cleanPolicyFields(p *domain.EscalationPolicy) (fieldErrors, []string) {
	errs := fieldErrors{}
	var order []string
	add := func(field, msg string) {
		order = append(order, field)
		errs.add(field, msg)
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	switch {
	case p.Name == "":
		add("name", "name is required")
	case utf8.RuneCountInString(p.Name) > maxPolicyName:
		add("name", "name must be at most 200 characters")
	}
	if utf8.RuneCountInString(p.Description) > maxPolicyDescription {
		add("description", "description must be at most 2000 characters")
	}
	return errs, order
}

type EscalationHandler struct {
	store store.Store
}

func NewEscalationHandler(s store.Store) *EscalationHandler {
	return &EscalationHandler{store: s}
}

func (h *EscalationHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	policies, err := h.store.EscalationPolicies().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list escalation policies")
		return
	}
	jsonResponse(w, http.StatusOK, policies)
}

func (h *EscalationHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	policy, err := h.store.EscalationPolicies().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if policy.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}

	// Include steps, their targets, and the open alert count (the SPA warns
	// before saving steps while alerts are in flight).
	type policyWithSteps struct {
		*domain.EscalationPolicy
		Steps          []stepWithTargets `json:"steps"`
		OpenAlertCount int               `json:"open_alert_count"`
	}

	steps, err := loadSteps(r.Context(), h.store, id)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load escalation steps")
		return
	}
	openAlerts, err := h.store.EscalationPolicies().CountOpenAlerts(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to count open alerts")
		return
	}

	jsonResponse(w, http.StatusOK, policyWithSteps{EscalationPolicy: policy, Steps: steps, OpenAlertCount: openAlerts})
}

func (h *EscalationHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	var policy domain.EscalationPolicy
	if err := decodeBody(r, &policy); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	policy.ID = uuid.New().String()
	policy.OrgID = claims.OrgID
	if errs, order := cleanPolicyFields(&policy); len(order) > 0 {
		writeFieldErrors(w, order, errs)
		return
	}

	if err := checkEscalationPolicyLimit(r.Context(), h.store, claims.OrgID); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	if err := validateEscalationPolicy(r.Context(), h.store, claims.OrgID, &policy); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	if err := h.store.EscalationPolicies().Create(r.Context(), &policy); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create escalation policy")
		return
	}
	jsonResponse(w, http.StatusCreated, &policy)
}

func (h *EscalationHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	policy, err := h.store.EscalationPolicies().GetByID(r.Context(), id)
	if err != nil || policy.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Loop        *bool   `json:"loop"`
		MaxLoops    *int    `json:"max_loops"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		policy.Name = *req.Name
	}
	if req.Description != nil {
		policy.Description = *req.Description
	}
	if errs, order := cleanPolicyFields(policy); len(order) > 0 {
		writeFieldErrors(w, order, errs)
		return
	}
	if req.Loop != nil {
		policy.Loop = *req.Loop
	}
	if req.MaxLoops != nil {
		policy.MaxLoops = req.MaxLoops
	}

	if err := validateEscalationPolicy(r.Context(), h.store, claims.OrgID, policy); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	if err := h.store.EscalationPolicies().Update(r.Context(), policy); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update escalation policy")
		return
	}
	jsonResponse(w, http.StatusOK, policy)
}

func (h *EscalationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	policy, err := h.store.EscalationPolicies().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if policy.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	if err := h.store.EscalationPolicies().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type replaceStepsRequest struct {
	Steps []stepWithTargets `json:"steps"`
}

// ReplaceSteps swaps a policy's steps. Step and target ids that already
// belong to the policy are kept so in-flight alerts stay on their step.
// Targets and channels are checked against the caller's org; problems come
// back as field-level 400s. The response is the saved steps with targets.
func (h *EscalationHandler) ReplaceSteps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	policyID := chi.URLParam(r, "id")
	policy, err := h.store.EscalationPolicies().GetByID(ctx, policyID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	claims := middleware.GetClaims(ctx)
	if policy.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	var req replaceStepsRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if errs, order := validateSteps(ctx, h.store, claims.OrgID, req.Steps); len(order) > 0 {
		writeFieldErrors(w, order, errs)
		return
	}

	current, err := loadSteps(ctx, h.store, policyID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load escalation steps")
		return
	}
	existingSteps := map[string]bool{}
	existingTargets := map[string]bool{}
	for _, st := range current {
		existingSteps[st.ID] = true
		for _, t := range st.Targets {
			existingTargets[t.ID] = true
		}
	}

	steps, targets := assignStepIDs(policyID, req.Steps, existingSteps, existingTargets)
	if err := h.store.EscalationPolicies().ReplaceSteps(ctx, policyID, steps, targets); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to replace steps")
		return
	}

	saved, err := loadSteps(ctx, h.store, policyID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load escalation steps")
		return
	}
	jsonResponse(w, http.StatusOK, saved)
}
