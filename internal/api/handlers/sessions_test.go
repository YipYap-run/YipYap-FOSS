package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// sessEnv is a full server for session revocation tests. Requests use the
// Authorization header with no cookie jar, so each token is its own client.
type sessEnv struct {
	t   *testing.T
	ts  *httptest.Server
	s   *sqlite.SQLiteStore
	jwt *auth.JWTIssuer
}

const sessOwnerEmail = "owner@sessions.test"
const sessOwnerPassword = "owner-password-1"

func newSessEnv(t *testing.T) *sessEnv {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	jwt := auth.NewJWTIssuer([]byte("sessions-test-secret"), time.Hour)
	ts := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{RegistrationEnabled: true, APIKeyHasher: auth.NewAPIKeyHasher([]byte("sessions-test-hasher"))}))
	t.Cleanup(ts.Close)
	e := &sessEnv{t: t, ts: ts, s: s, jwt: jwt}
	code, body := e.do("", http.MethodPost, "/api/v1/auth/register", map[string]string{
		"org_name": "Sessions Org", "email": sessOwnerEmail, "password": sessOwnerPassword,
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	return e
}

func (e *sessEnv) do(token, method, path string, payload interface{}) (int, []byte) {
	e.t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *sessEnv) login(email, password string) string {
	e.t.Helper()
	code, body := e.do("", http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", email, code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Token == "" {
		e.t.Fatalf("login %s: no token", email)
	}
	return out.Token
}

func (e *sessEnv) sid(token string) string {
	e.t.Helper()
	c, err := e.jwt.Validate(token)
	if err != nil {
		e.t.Fatal(err)
	}
	return c.SID
}

// works reports whether token can still call an authenticated route.
func (e *sessEnv) works(token string) bool {
	e.t.Helper()
	code, body := e.do(token, http.MethodGet, "/api/v1/org", nil)
	switch code {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		var out map[string]string
		_ = json.Unmarshal(body, &out)
		if out["code"] != "session_revoked" {
			e.t.Fatalf("401 without session_revoked code: %s", body)
		}
		return false
	default:
		e.t.Fatalf("GET /org: unexpected %d %s", code, body)
		return false
	}
}

func (e *sessEnv) addMember(_ string, email, password, role string) string {
	e.t.Helper()
	ctx := context.Background()
	owner, err := e.s.Users().GetByEmail(ctx, sessOwnerEmail)
	if err != nil {
		e.t.Fatal(err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		e.t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	u := &domain.User{
		ID: owner.ID + "-" + role + "-" + email, OrgID: owner.OrgID, Email: email,
		Name: email, PasswordHash: hash, Role: domain.UserRole(role), CreatedAt: now, UpdatedAt: now,
	}
	if err := e.s.Users().Create(ctx, u); err != nil {
		e.t.Fatalf("create user: %v", err)
	}
	if err := e.s.Users().MarkEmailVerified(ctx, u.ID, now); err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func TestSessions_TokenWithoutSIDRejected(t *testing.T) {
	e := newSessEnv(t)
	u, err := e.s.Users().GetByEmail(context.Background(), sessOwnerEmail)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := e.jwt.Issue(u.ID, u.OrgID, string(u.Role))
	if err != nil {
		t.Fatal(err)
	}
	if e.works(legacy) {
		t.Fatal("token without a sid was accepted")
	}
}

func TestSessions_LogoutRevokes(t *testing.T) {
	e := newSessEnv(t)
	tok := e.login(sessOwnerEmail, sessOwnerPassword)
	other := e.login(sessOwnerEmail, sessOwnerPassword)
	if !e.works(tok) {
		t.Fatal("fresh session rejected")
	}
	if code, body := e.do(tok, http.MethodPost, "/api/v1/auth/logout", nil); code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", code, body)
	}
	if e.works(tok) {
		t.Fatal("token still accepted after logout")
	}
	if !e.works(other) {
		t.Fatal("logout revoked a different session")
	}
	if code, _ := e.do(tok, http.MethodPost, "/api/v1/auth/refresh", nil); code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want 401", code)
	}
}

func TestSessions_PasswordChangeRevokesOthersOnly(t *testing.T) {
	e := newSessEnv(t)
	a := e.login(sessOwnerEmail, sessOwnerPassword)
	b := e.login(sessOwnerEmail, sessOwnerPassword)
	code, body := e.do(a, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current_password": sessOwnerPassword, "new_password": "brand-new-password-2",
	})
	if code != http.StatusOK {
		t.Fatalf("change password: %d %s", code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &out)
	if !e.works(a) || !e.works(out.Token) {
		t.Fatal("the session that changed the password was signed out")
	}
	if e.sid(out.Token) != e.sid(a) {
		t.Fatal("password change moved the current session to a new id")
	}
	if e.works(b) {
		t.Fatal("other session still accepted after password change")
	}
}

func TestSessions_PasswordResetRevokesAll(t *testing.T) {
	e := newSessEnv(t)
	a := e.login(sessOwnerEmail, sessOwnerPassword)
	u, _ := e.s.Users().GetByEmail(context.Background(), sessOwnerEmail)
	reset, err := e.jwt.IssuePasswordReset(u.ID, u.OrgID, u.Email, u.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.do("", http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
		"token": reset, "password": "reset-password-3",
	})
	if code != http.StatusOK {
		t.Fatalf("reset: %d %s", code, body)
	}
	if e.works(a) {
		t.Fatal("session still accepted after password reset")
	}
}

func TestSessions_AdminResetAndDemotionRevoke(t *testing.T) {
	e := newSessEnv(t)
	owner := e.login(sessOwnerEmail, sessOwnerPassword)
	memberID := e.addMember(owner, "member@sessions.test", "member-password-1", "member")
	viewerID := e.addMember(owner, "viewer@sessions.test", "viewer-password-1", "member")
	member := e.login("member@sessions.test", "member-password-1")
	viewer := e.login("viewer@sessions.test", "viewer-password-1")

	// Demotion.
	if code, body := e.do(owner, http.MethodPatch, "/api/v1/users/"+viewerID, map[string]string{"role": "viewer"}); code != http.StatusOK {
		t.Fatalf("demote: %d %s", code, body)
	}
	if e.works(viewer) {
		t.Fatal("token still accepted after demotion")
	}
	if !e.works(owner) || !e.works(member) {
		t.Fatal("demotion revoked unrelated sessions")
	}

	// An update that does not change the role keeps sessions.
	if code, body := e.do(owner, http.MethodPatch, "/api/v1/users/"+memberID, map[string]string{"name": "M"}); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, body)
	}
	if !e.works(member) {
		t.Fatal("a rename signed the user out")
	}

	// Admin password reset.
	if code, body := e.do(owner, http.MethodPost, "/api/v1/users/"+memberID+"/reset-password", map[string]string{"temporary_password": "temporary-pass-1"}); code != http.StatusOK {
		t.Fatalf("admin reset: %d %s", code, body)
	}
	if e.works(member) {
		t.Fatal("token still accepted after admin password reset")
	}
}

func TestSessions_DisableRevokes(t *testing.T) {
	e := newSessEnv(t)
	tok := e.login(sessOwnerEmail, sessOwnerPassword)
	u, _ := e.s.Users().GetByEmail(context.Background(), sessOwnerEmail)
	del, err := e.jwt.IssueAccountDeletion(u.ID, u.OrgID, u.Email, u.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := e.do("", http.MethodPost, "/api/v1/auth/confirm-delete", map[string]string{"token": del}); code != http.StatusOK {
		t.Fatalf("confirm delete: %d %s", code, body)
	}
	// Bypass the disabled-account guard to prove the session itself is gone.
	c, _ := e.jwt.Validate(tok)
	got, err := e.s.Sessions().Get(context.Background(), c.SID)
	if err != nil || got.RevokedAt == nil {
		t.Fatalf("session not revoked after disable: %+v %v", got, err)
	}
	if code, _ := e.do(tok, http.MethodGet, "/api/v1/org", nil); code != http.StatusUnauthorized {
		t.Fatalf("GET /org after disable = %d, want 401", code)
	}
}

type sessionListResp struct {
	Sessions []struct {
		ID         string `json:"id"`
		AuthMethod string `json:"auth_method"`
		Current    bool   `json:"current"`
	} `json:"sessions"`
}

func TestSessions_ListAndRevokeScopedToCaller(t *testing.T) {
	e := newSessEnv(t)
	a := e.login(sessOwnerEmail, sessOwnerPassword)
	b := e.login(sessOwnerEmail, sessOwnerPassword)
	c := e.login(sessOwnerEmail, sessOwnerPassword)
	e.addMember(a, "member@sessions.test", "member-password-1", "member")
	member := e.login("member@sessions.test", "member-password-1")

	code, body := e.do(a, http.MethodGet, "/api/v1/auth/sessions", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list sessionListResp
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	// Register created one session too.
	if len(list.Sessions) != 4 {
		t.Fatalf("owner sees %d sessions, want 4: %s", len(list.Sessions), body)
	}
	current := 0
	for _, s := range list.Sessions {
		if s.ID == e.sid(member) {
			t.Fatal("owner's list includes the member's session")
		}
		if s.Current {
			current++
			if s.ID != e.sid(a) {
				t.Fatal("wrong session marked current")
			}
		}
		if s.AuthMethod != "password" {
			t.Errorf("auth_method = %q", s.AuthMethod)
		}
	}
	if current != 1 {
		t.Fatalf("%d sessions marked current", current)
	}

	// A user cannot revoke someone else's session.
	if code, _ := e.do(member, http.MethodDelete, "/api/v1/auth/sessions/"+e.sid(b), nil); code != http.StatusNotFound {
		t.Fatalf("cross-user revoke = %d, want 404", code)
	}
	if !e.works(b) {
		t.Fatal("cross-user revoke took effect")
	}

	// Revoke one of my own.
	if code, body := e.do(a, http.MethodDelete, "/api/v1/auth/sessions/"+e.sid(b), nil); code != http.StatusNoContent {
		t.Fatalf("revoke own: %d %s", code, body)
	}
	if e.works(b) {
		t.Fatal("revoked session still accepted")
	}
	if code, _ := e.do(a, http.MethodDelete, "/api/v1/auth/sessions/"+e.sid(b), nil); code != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", code)
	}

	// Sign out everywhere else.
	if code, body := e.do(a, http.MethodPost, "/api/v1/auth/sessions/revoke-others", nil); code != http.StatusNoContent {
		t.Fatalf("revoke others: %d %s", code, body)
	}
	if !e.works(a) {
		t.Fatal("revoke-others signed out the current session")
	}
	if e.works(c) {
		t.Fatal("revoke-others left another session active")
	}
	if !e.works(member) {
		t.Fatal("revoke-others touched another user's session")
	}

	// Revoking the current session signs it out.
	if code, _ := e.do(a, http.MethodDelete, "/api/v1/auth/sessions/"+e.sid(a), nil); code != http.StatusNoContent {
		t.Fatalf("revoke current = %d", code)
	}
	if e.works(a) {
		t.Fatal("current session still accepted after revoking it")
	}
}

func TestSessions_APIKeyCannotManageSessions(t *testing.T) {
	e := newSessEnv(t)
	owner := e.login(sessOwnerEmail, sessOwnerPassword)
	code, body := e.do(owner, http.MethodPost, "/api/v1/org/api-keys", map[string]interface{}{"name": "k", "scopes": []string{"org:read"}})
	if code != http.StatusCreated {
		t.Fatalf("api key create: %d %s", code, body)
	}
	var k struct {
		Key   string `json:"key"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &k)
	key := k.Key
	if key == "" {
		key = k.Token
	}
	if key == "" {
		t.Fatalf("no key in %s", body)
	}
	if code, _ := e.do(key, http.MethodGet, "/api/v1/auth/sessions", nil); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Fatalf("API key listed sessions: %d", code)
	}
}
