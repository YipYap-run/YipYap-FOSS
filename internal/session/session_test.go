package session

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type env struct {
	s   *sqlite.SQLiteStore
	jwt *auth.JWTIssuer
	org *domain.Org
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-1", Name: "Org", Slug: "org", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	return &env{s: s, jwt: auth.NewJWTIssuer([]byte("session-test-secret"), time.Hour), org: org}
}

func (e *env) issue(t *testing.T, userID string) *auth.Claims {
	t.Helper()
	tok, err := Issue(context.Background(), e.s, e.jwt, nil, userID, e.org.ID, "member", auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.jwt.Validate(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.SID == "" {
		t.Fatal("issued token has no sid")
	}
	return c
}

func withClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	nowFunc = func() time.Time { return cur }
	t.Cleanup(func() { nowFunc = time.Now })
	return &cur
}

func TestCacheTTLIsBounded(t *testing.T) {
	if CacheTTL <= 0 || CacheTTL > 30*time.Second {
		t.Fatalf("CacheTTL = %v, must be in (0, 30s]", CacheTTL)
	}
}

func TestIssueStoresRowWithRequestDetails(t *testing.T) {
	e := newEnv(t)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:4567"
	r.Header.Set("User-Agent", strings.Repeat("a", 2000))
	tok, err := Issue(context.Background(), e.s, e.jwt, r, "u1", e.org.ID, "member", auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := e.jwt.Validate(tok)
	got, err := e.s.Sessions().Get(context.Background(), c.SID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "u1" || got.OrgID != e.org.ID || got.AuthMethod != domain.SessionAuthPassword {
		t.Fatalf("row = %+v", got)
	}
	if got.IP != "203.0.113.9" {
		t.Errorf("ip = %q", got.IP)
	}
	if len(got.UserAgent) != maxUserAgentLen {
		t.Errorf("user agent length = %d, want %d", len(got.UserAgent), maxUserAgentLen)
	}
	if !got.ExpiresAt.Equal(c.ExpiresAt.Time) {
		t.Errorf("row expires %v, token expires %v", got.ExpiresAt, c.ExpiresAt.Time)
	}
}

func TestCheckRejectsMissingRevokedMismatchedExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := Check(ctx, e.s, &auth.Claims{UserID: "u1", OrgID: e.org.ID}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("token without sid: err = %v", err)
	}
	if err := Check(ctx, e.s, &auth.Claims{UserID: "u1", OrgID: e.org.ID, SID: "nope"}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("unknown sid: err = %v", err)
	}

	c := e.issue(t, "u1")
	if err := Check(ctx, e.s, c); err != nil {
		t.Fatalf("fresh session rejected: %v", err)
	}
	forged := *c
	forged.UserID = "u2"
	if err := Check(ctx, e.s, &forged); !errors.Is(err, ErrRevoked) {
		t.Fatalf("sid reused for another user: err = %v", err)
	}

	// Revocation on this replica lands at once, even with a warm cache.
	if err := Revoke(ctx, e.s, "u1", c.SID); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, e.s, c); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked session accepted: %v", err)
	}

	c2 := e.issue(t, "u1")
	withClock(t, time.Now().Add(2*time.Hour))
	if err := Check(ctx, e.s, c2); !errors.Is(err, ErrRevoked) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestRevocationFromAnotherReplicaLandsWithinCacheTTL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.issue(t, "u1")
	clock := withClock(t, time.Now())
	if err := Check(ctx, e.s, c); err != nil {
		t.Fatal(err)
	}
	// Another replica revokes straight in the database; this process's
	// cache does not hear about it.
	if err := e.s.Sessions().Revoke(ctx, "u1", c.SID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(CacheTTL)
	if err := Check(ctx, e.s, c); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revocation not seen after CacheTTL: %v", err)
	}
}

func TestLastSeenWrittenAtMostOncePerMinute(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.issue(t, "u1")
	start, _ := e.s.Sessions().Get(ctx, c.SID)

	clock := withClock(t, start.LastSeenAt.Add(30*time.Second))
	forget(c.SID)
	if err := Check(ctx, e.s, c); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.Sessions().Get(ctx, c.SID)
	if !got.LastSeenAt.Equal(start.LastSeenAt) {
		t.Fatalf("last_seen_at moved after 30s: %v -> %v", start.LastSeenAt, got.LastSeenAt)
	}

	*clock = start.LastSeenAt.Add(61 * time.Second)
	forget(c.SID)
	if err := Check(ctx, e.s, c); err != nil {
		t.Fatal(err)
	}
	got, _ = e.s.Sessions().Get(ctx, c.SID)
	want := clock.UTC().Truncate(time.Second)
	if !got.LastSeenAt.Equal(want) {
		t.Fatalf("last_seen_at = %v, want %v", got.LastSeenAt, want)
	}
}

func TestRevokeAllKeepsCurrentAndScopesToUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.issue(t, "u1")
	b := e.issue(t, "u1")
	other := e.issue(t, "u2")
	for _, c := range []*auth.Claims{a, b, other} {
		if err := Check(ctx, e.s, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := RevokeAll(ctx, e.s, "u1", a.SID); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, e.s, a); err != nil {
		t.Fatalf("kept session rejected: %v", err)
	}
	if err := Check(ctx, e.s, b); !errors.Is(err, ErrRevoked) {
		t.Fatalf("other session of the user accepted: %v", err)
	}
	if err := Check(ctx, e.s, other); err != nil {
		t.Fatalf("another user's session rejected: %v", err)
	}

	// Revoking someone else's session is a not-found.
	if err := Revoke(ctx, e.s, "u1", other.SID); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatalf("cross-user revoke err = %v", err)
	}
	if err := Check(ctx, e.s, other); err != nil {
		t.Fatalf("cross-user revoke took effect: %v", err)
	}

	list, err := List(ctx, e.s, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.SID {
		t.Fatalf("active sessions = %+v", list)
	}
}

func TestRenewKeepsSessionID(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.issue(t, "u1")
	tok, err := Renew(ctx, e.s, e.jwt, c, "admin")
	if err != nil {
		t.Fatal(err)
	}
	nc, _ := e.jwt.Validate(tok)
	if nc.SID != c.SID || nc.Role != "admin" || nc.AMR != c.AMR {
		t.Fatalf("renewed claims = %+v", nc)
	}
	if err := Revoke(ctx, e.s, "u1", c.SID); err != nil {
		t.Fatal(err)
	}
	if _, err := Renew(ctx, e.s, e.jwt, c, "admin"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("renewed a revoked session: %v", err)
	}
}

func TestPruneExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	if err := e.s.Sessions().Create(ctx, &domain.Session{
		ID: "old", UserID: "u1", OrgID: e.org.ID, AuthMethod: "password",
		CreatedAt: old, LastSeenAt: old, ExpiresAt: old.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	live := e.issue(t, "u1")
	n, err := PruneExpired(ctx, e.s, time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, err %v", n, err)
	}
	if _, err := e.s.Sessions().Get(ctx, live.SID); err != nil {
		t.Fatalf("live session pruned: %v", err)
	}
}
