package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// issueLoginSession creates the server-side session for a completed login
// and returns its JWT. Every login path goes through here so each token
// carries a revocable sid.
func issueLoginSession(r *http.Request, s store.Store, jwt *auth.JWTIssuer, user *domain.User, amr string) (string, error) {
	return session.Issue(r.Context(), s, jwt, r, user.ID, user.OrgID, string(user.Role), amr)
}
