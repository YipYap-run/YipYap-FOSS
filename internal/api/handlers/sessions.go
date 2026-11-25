package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SessionHandler lists and revokes the signed-in user's own sessions.
type SessionHandler struct {
	store store.Store
}

// NewSessionHandler returns a SessionHandler.
func NewSessionHandler(s store.Store) *SessionHandler {
	return &SessionHandler{store: s}
}

type sessionView struct {
	ID         string    `json:"id"`
	AuthMethod string    `json:"auth_method"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

// ownSessionClaims returns the caller's claims, or writes an error. Read-only
// operator sessions share one synthetic user across orgs, so they cannot list
// or revoke sessions.
func ownSessionClaims(w http.ResponseWriter, r *http.Request) (userID, sid string, ok bool) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.SID == "" {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return "", "", false
	}
	if middleware.IsStaffReadonly(claims) {
		errorResponse(w, http.StatusForbidden, "not available in this session")
		return "", "", false
	}
	return claims.UserID, claims.SID, true
}

// List handles GET /auth/sessions. It returns the caller's active sessions
// and marks the one making the request.
func (h *SessionHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, sid, ok := ownSessionClaims(w, r)
	if !ok {
		return
	}
	rows, err := session.List(r.Context(), h.store, userID)
	if err != nil {
		slog.Error("sessions: list", "user_id", userID, "error", err)
		errorResponse(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	out := make([]sessionView, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionView{
			ID:         s.ID,
			AuthMethod: s.AuthMethod,
			CreatedAt:  s.CreatedAt,
			LastSeenAt: s.LastSeenAt,
			ExpiresAt:  s.ExpiresAt,
			UserAgent:  s.UserAgent,
			IP:         s.IP,
			Current:    s.ID == sid,
		})
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"sessions": out})
}

// Revoke handles DELETE /auth/sessions/{id}. A user can revoke only their
// own sessions; any other ID is a 404. Revoking the current session also
// clears its cookie.
func (h *SessionHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID, sid, ok := ownSessionClaims(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" || len(id) > 64 {
		errorResponse(w, http.StatusNotFound, "session not found")
		return
	}
	if err := session.Revoke(r.Context(), h.store, userID, id); err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			errorResponse(w, http.StatusNotFound, "session not found")
			return
		}
		slog.Error("sessions: revoke", "user_id", userID, "error", err)
		errorResponse(w, http.StatusInternalServerError, "failed to revoke session")
		return
	}
	if id == sid {
		clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOthers handles POST /auth/sessions/revoke-others: sign out
// everywhere except the current session.
func (h *SessionHandler) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	userID, sid, ok := ownSessionClaims(w, r)
	if !ok {
		return
	}
	if err := session.RevokeAll(r.Context(), h.store, userID, sid); err != nil {
		slog.Error("sessions: revoke others", "user_id", userID, "error", err)
		errorResponse(w, http.StatusInternalServerError, "failed to sign out other sessions")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
