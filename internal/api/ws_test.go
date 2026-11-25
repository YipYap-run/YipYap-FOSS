package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// TestHubConcurrentRegisterUnregisterBroadcast exercises the hub the way a
// busy web process does: tabs opening and closing while alerts fan out.
// Run with -race. Before mon-5 this raced on the inner org map and could
// panic sending on a closed channel.
func TestHubConcurrentRegisterUnregisterBroadcast(t *testing.T) {
	h := NewHub("")
	const workers = 16
	const rounds = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Broadcasters.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.broadcast("org-1", []byte(`{"type":"alert.fired"}`))
				}
			}
		}()
	}

	// Clients connecting and tearing down in the ServeWS order, with a
	// reader draining send like the write pump does.
	var cw sync.WaitGroup
	for i := 0; i < workers; i++ {
		cw.Add(1)
		go func() {
			defer cw.Done()
			for j := 0; j < rounds; j++ {
				c := newClient("org-1")
				h.register(c)
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					for {
						select {
						case <-c.done:
							return
						case <-c.send:
						}
					}
				}()
				h.unregister(c)
				c.stop()
				c.stop() // idempotent
				<-drained
			}
		}()
	}
	cw.Wait()
	close(stop)
	wg.Wait()

	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.clients) != 0 || len(h.byOrg) != 0 {
		t.Fatalf("hub not empty after teardown: clients=%d orgs=%d", len(h.clients), len(h.byOrg))
	}
}

// TestHubBroadcastSkipsStoppedClient checks that a client stopped but not
// yet unregistered is skipped instead of being written to.
func TestHubBroadcastSkipsStoppedClient(t *testing.T) {
	h := NewHub("")
	c := newClient("org-1")
	c.send = make(chan []byte) // unbuffered: a send would only succeed with a reader
	h.register(c)
	c.stop()
	done := make(chan struct{})
	go func() {
		h.broadcast("org-1", []byte("x"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a stopped client")
	}
}

// overlayStore wraps a real store and lets a test patch the user record
// GetByID returns, for fields the shared user store does not write back
// (for example mfa_enforced_at).
type overlayStore struct {
	store.Store
	mu      sync.Mutex
	patches map[string]func(*domain.User)
}

func (o *overlayStore) patch(id string, fn func(*domain.User)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.patches[id] = fn
}

func (o *overlayStore) Users() store.UserStore {
	return &overlayUsers{UserStore: o.Store.Users(), o: o}
}

func (o *overlayStore) Sessions() store.SessionStore {
	return o.Store.(store.SessionProvider).Sessions()
}

type overlayUsers struct {
	store.UserStore
	o *overlayStore
}

func (u *overlayUsers) GetByID(ctx context.Context, id string) (*domain.User, error) {
	usr, err := u.UserStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	u.o.mu.Lock()
	fn := u.o.patches[id]
	u.o.mu.Unlock()
	if fn != nil {
		fn(usr)
	}
	return usr, nil
}

const wsTestSecret = "ws-test-secret"

type wsFixture struct {
	srv   *httptest.Server
	store *overlayStore
	jwt   *auth.JWTIssuer
	hub   *Hub
	org   *domain.Org
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-ws", Name: "WS", Slug: "ws", Plan: domain.OrgPlan("pro"), CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatalf("create org: %v", err)
	}

	jwt := auth.NewJWTIssuer([]byte(wsTestSecret), time.Hour)
	hub := NewHub("")
	hub.revalidateEvery = 50 * time.Millisecond
	ovl := &overlayStore{Store: s, patches: map[string]func(*domain.User){}}
	srv := httptest.NewServer(hub.ServeWS(jwt, ovl, session.Checker(s)))
	t.Cleanup(srv.Close)
	return &wsFixture{srv: srv, store: ovl, jwt: jwt, hub: hub, org: org}
}

func (f *wsFixture) addUser(t *testing.T, id string, mutate func(u *domain.User)) *domain.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u := &domain.User{
		ID:           id,
		OrgID:        f.org.ID,
		Email:        id + "@example.com",
		PasswordHash: "x",
		Role:         domain.RoleMember,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if mutate != nil {
		mutate(u)
	}
	if err := f.store.Users().Create(context.Background(), u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func (f *wsFixture) token(t *testing.T, u *domain.User) string {
	t.Helper()
	tok, err := session.Issue(context.Background(), f.store, f.jwt, nil, u.ID, u.OrgID, string(u.Role), auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *wsFixture) wsURL() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") }

func (f *wsFixture) dial(t *testing.T, cookie, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	if cookie != "" {
		hdr.Set("Cookie", "yipyap_session="+cookie)
	}
	u := f.wsURL()
	if query != "" {
		u += "?" + query
	}
	return websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
}

func expectRejected(t *testing.T, conn *websocket.Conn, resp *http.Response, err error, want int) {
	t.Helper()
	if err == nil {
		_ = conn.CloseNow()
		t.Fatalf("expected handshake to be rejected with %d, it was accepted", want)
	}
	if resp == nil {
		t.Fatalf("expected HTTP %d, got no response: %v", want, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("expected HTTP %d, got %d", want, resp.StatusCode)
	}
}

func TestServeWSAcceptsSessionCookie(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "ok", nil)
	conn, _, err := f.dial(t, f.token(t, u), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// The client is registered and receives org broadcasts.
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.hub.mu.RLock()
		n := len(f.hub.byOrg[f.org.ID])
		f.hub.mu.RUnlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.hub.broadcast(f.org.ID, []byte(`{"type":"alert.fired"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != `{"type":"alert.fired"}` {
		t.Fatalf("unexpected frame %q", msg)
	}
}

func TestServeWSRejectsQueryToken(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "q", nil)
	conn, resp, err := f.dial(t, "", "token="+f.token(t, u))
	expectRejected(t, conn, resp, err, http.StatusUnauthorized)
}

func TestServeWSRejectsDisabledUser(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "off", nil)
	if err := f.store.Users().Disable(context.Background(), u.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	conn, resp, err := f.dial(t, f.token(t, u), "")
	expectRejected(t, conn, resp, err, http.StatusForbidden)
}

func TestServeWSRejectsForcedPasswordChange(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "pw", func(u *domain.User) { u.ForcePasswordChange = true })
	conn, resp, err := f.dial(t, f.token(t, u), "")
	expectRejected(t, conn, resp, err, http.StatusForbidden)
}

func TestServeWSRejectsRestrictedAudienceToken(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "restricted-tok", nil)
	tok, err := f.jwt.IssueEmailVerification(u.ID, u.OrgID, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	conn, resp, err := f.dial(t, tok, "")
	expectRejected(t, conn, resp, err, http.StatusUnauthorized)
}

// waitClosed reads until the server closes the connection and returns the
// close status.
func waitClosed(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("connection stayed open")
			}
			return websocket.CloseStatus(err)
		}
	}
}

func TestServeWSClosesWhenUserDisabled(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "later-off", nil)
	conn, _, err := f.dial(t, f.token(t, u), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if err := f.store.Users().Disable(context.Background(), u.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := waitClosed(t, conn); got != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want policy violation", got)
	}
}

func TestServeWSClosesWhenSessionExpires(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "short", nil)
	f.hub.revalidateEvery = time.Hour // only the expiry timer can close it
	shortJwt := auth.NewJWTIssuer([]byte(wsTestSecret), 1500*time.Millisecond)
	tok, err := session.Issue(context.Background(), f.store, shortJwt, nil, u.ID, u.OrgID, string(u.Role), auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := f.dial(t, tok, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if got := waitClosed(t, conn); got != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want policy violation", got)
	}
}

func TestServeWSClosesWhenPasswordChangeForced(t *testing.T) {
	f := newWSFixture(t)
	u := f.addUser(t, "later-pw", nil)
	conn, _, err := f.dial(t, f.token(t, u), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	f.store.patch(u.ID, func(u *domain.User) { u.ForcePasswordChange = true })
	if got := waitClosed(t, conn); got != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want policy violation", got)
	}
}
