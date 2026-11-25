package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestWSChecksSessionAndClosesOnRevocation(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Orgs().Create(ctx, &domain.Org{ID: "org-ws", Name: "WS", Slug: "ws", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	jwt := auth.NewJWTIssuer([]byte("ws-session-secret"), time.Hour)
	if err := s.Users().Create(ctx, &domain.User{ID: "u1", OrgID: "org-ws", Email: "u1@example.com", PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	old := wsSessionRecheck
	wsSessionRecheck = 20 * time.Millisecond
	t.Cleanup(func() { wsSessionRecheck = old })

	hub := NewHub("")
	srv := httptest.NewServer(hub.ServeWS(jwt, s, session.Checker(s)))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	dial := func(tok string) (*websocket.Conn, *http.Response, error) {
		h := http.Header{}
		h.Set("Cookie", "yipyap_session="+tok)
		dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: h})
	}

	legacy, _ := jwt.Issue("u1", "org-ws", "member")
	if _, resp, err := dial(legacy); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token without a session upgraded: err=%v", err)
	}

	tok, err := session.Issue(ctx, s, jwt, nil, "u1", "org-ws", "member", auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := dial(tok)
	if err != nil {
		t.Fatalf("active session could not connect: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	c, _ := jwt.Validate(tok)
	if err := session.Revoke(ctx, s, "u1", c.SID); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _, err = conn.Read(rctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("socket not closed for revocation: %v", err)
	}

	if _, resp, err := dial(tok); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session upgraded: err=%v", err)
	}
}
