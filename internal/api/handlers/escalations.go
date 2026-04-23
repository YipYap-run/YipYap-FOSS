package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

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

	// Include steps and their targets in the response.
	type stepWithTargets struct {
		domain.EscalationStep
		Targets []domain.StepTarget `json:"targets"`
	}
	type policyWithSteps struct {
		*domain.EscalationPolicy
		Steps []stepWithTargets `json:"steps"`
	}

	result := policyWithSteps{EscalationPolicy: policy}
	steps, err := h.store.EscalationPolicies().GetSteps(r.Context(), id)
	if err == nil {
		for _, step := range steps {
			st := stepWithTargets{EscalationStep: step}
			targets, err := h.store.EscalationPolicies().GetTargets(r.Context(), step.ID)
			if err == nil {
				st.Targets = targets
			}
			result.Steps = append(result.Steps, st)
		}
	}

	jsonResponse(w, http.StatusOK, result)
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
		Name     *string `json:"name"`
		Loop     *bool   `json:"loop"`
		MaxLoops *int    `json:"max_loops"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		policy.Name = *req.Name
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
	Steps []struct {
		domain.EscalationStep
		Targets []domain.StepTarget `json:"targets"`
	} `json:"steps"`
}

func (h *EscalationHandler) ReplaceSteps(w http.ResponseWriter, r *http.Request) {
	policyID := chi.URLParam(r, "id")
	policy, err := h.store.EscalationPolicies().GetByID(r.Context(), policyID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if policy.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "escalation policy not found")
		return
	}
	var req replaceStepsRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	steps := make([]domain.EscalationStep, len(req.Steps))
	targets := make(map[string][]domain.StepTarget)
	for i, s := range req.Steps {
		if s.ID == "" {
			s.ID = uuid.New().String()
		}
		s.PolicyID = policyID
		s.Position = i
		steps[i] = s.EscalationStep
		if len(s.Targets) > 0 {
			for j := range s.Targets {
				if s.Targets[j].ID == "" {
					s.Targets[j].ID = uuid.New().String()
				}
				s.Targets[j].StepID = s.ID
			}
			targets[s.ID] = s.Targets
		}
	}

	if err := h.store.EscalationPolicies().ReplaceSteps(r.Context(), policyID, steps, targets); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to replace steps")
		return
	}
	jsonResponse(w, http.StatusOK, steps)
}
