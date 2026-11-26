package api_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestServerAccessLogHasNoSecrets covers core-5 end to end: the server's
// global access log must not contain a token path parameter or a
// ?token= query value.
func TestServerAccessLogHasNoSecrets(t *testing.T) {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	// Also capture chi's stdout logger so re-adding chimw.Logger fails here.
	prevChi := chimw.DefaultLogger
	chimw.DefaultLogger = chimw.RequestLogger(&chimw.DefaultLogFormatter{Logger: log.New(buf, "", 0), NoColor: true})
	t.Cleanup(func() { chimw.DefaultLogger = prevChi })

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)
	hub := api.NewHub("")
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), hub, nil, "", "", api.ServerOptions{}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/ack/path-secret-abc?token=query-secret-xyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// The ?token= WebSocket fallback is gone: a valid JWT in the query
	// must not authenticate, and must not be logged.
	tok, err := jwt.Issue("user-1", "org-1", "admin")
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(srv.URL + "/ws?token=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/ws with ?token= = %d, want 401", resp.StatusCode)
	}

	out := buf.String()
	for name, secret := range map[string]string{"path token": "path-secret-abc", "query token": "query-secret-xyz", "ws JWT": tok} {
		if strings.Contains(out, secret) {
			t.Errorf("server log leaks the %s", name)
		}
	}
	if !strings.Contains(out, `"route":"/ack/{token}"`) {
		t.Errorf("access log missing ack route pattern: %s", out)
	}
	if !strings.Contains(out, `"route":"/ws"`) {
		t.Errorf("access log missing /ws route: %s", out)
	}
}

// TestServerWebSocketCookieUpgrade checks that the cookie-only /ws path still
// upgrades through the full middleware stack. RequestLog wraps the
// ResponseWriter, and the upgrade must still be able to hijack it. (core-5)
func TestServerWebSocketCookieUpgrade(t *testing.T) {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-1", Name: "WS", Slug: "ws-1", Plan: domain.OrgPlan("pro"), CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: "user-1", OrgID: org.ID, Email: "ws@example.com", PasswordHash: "x", Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), api.NewHub(""), nil, "", "", api.ServerOptions{}))
	t.Cleanup(srv.Close)

	tok, err := session.Issue(context.Background(), s, jwt, nil, u.ID, u.OrgID, string(u.Role), auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Cookie", "yipyap_session="+tok)
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws",
		&websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial /ws with session cookie: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade status = %d, want 101", resp.StatusCode)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// The access line is written when the handler returns, after close.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), `"route":"/ws","status":101`) {
		if time.Now().After(deadline) {
			t.Fatalf("no 101 access log line for /ws: %s", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(buf.String(), tok) {
		t.Error("access log leaks the session JWT")
	}
}
