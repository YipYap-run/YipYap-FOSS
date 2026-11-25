package handlers

import (
	"context"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// userReadiness says whether the escalation engine can page a person right
// now. Types lists the verified method types that have a rule, including
// ones the plan or this server cannot deliver, so the UI can say why.
type userReadiness struct {
	Pageable bool                       `json:"pageable"`
	Types    []domain.ContactMethodType `json:"types"`
}

// pagingReadiness maps user ID to readiness for every org member that has
// at least one rule on a verified method. A user missing from the map has
// nothing the engine can page. The second result is false when the store
// does not hold contact methods; callers then skip the warning.
//
// Pageable follows the engine's checks in deliverToMethod: the type must be
// deliverable by this build and allowed by the org's plan.
func pagingReadiness(ctx context.Context, s store.Store, orgID string) (map[string]userReadiness, bool, error) {
	cp, ok := s.(store.ContactMethodProvider)
	if !ok {
		return nil, false, nil
	}
	byUser, err := cp.NotificationRules().PagingTypesByOrg(ctx, orgID)
	if err != nil {
		return nil, true, err
	}
	allowed := map[domain.ContactMethodType]bool{}
	usable := func(t domain.ContactMethodType) bool {
		if v, seen := allowed[t]; seen {
			return v
		}
		v := contactmethod.TypeSupported(t) && contactmethod.PlanError(ctx, s, orgID, t) == nil
		allowed[t] = v
		return v
	}
	out := make(map[string]userReadiness, len(byUser))
	for userID, types := range byUser {
		r := userReadiness{Types: types}
		for _, t := range types {
			if usable(t) {
				r.Pageable = true
				break
			}
		}
		out[userID] = r
	}
	return out, true, nil
}

// PagingReadiness serves GET /users/paging-readiness for the on-call page
// and the escalation step editor, so they can warn about people who would
// not be paged without one request per member. Any signed-in org member may
// read it; it exposes only method types, never addresses.
func (h *ContactMethodHandler) PagingReadiness(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		errorResponse(w, http.StatusForbidden, "forbidden")
		return
	}
	users, ok, err := pagingReadiness(r.Context(), h.store, claims.OrgID)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "contact methods are not supported by this store")
		return
	}
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to check contact methods")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"users": users})
}
