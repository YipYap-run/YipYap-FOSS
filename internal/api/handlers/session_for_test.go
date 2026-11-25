package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// sessionFor creates the server-side session a sign-in would create and
// returns a token bound to it. The authed chain rejects session tokens that
// name no live session row.
func sessionFor(t *testing.T, s store.Store, issuer *auth.JWTIssuer, u *domain.User) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	sess := &domain.Session{
		ID:         uuid.NewString(),
		UserID:     u.ID,
		OrgID:      u.OrgID,
		AuthMethod: auth.AMRPassword,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
	sp, ok := s.(store.SessionProvider)
	if !ok {
		t.Fatal("store keeps no sessions")
	}
	if err := sp.Sessions().Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	tok, err := issuer.IssueSessionToken(u.ID, u.OrgID, string(u.Role), auth.AMRPassword, sess.ID, sess.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
