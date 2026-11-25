package handlers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type sentMail struct{ to, subject, body string }

type fakeInviteMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *fakeInviteMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to, subject, body})
	return nil
}

func (m *fakeInviteMailer) last(t *testing.T) sentMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no email sent")
	}
	return m.sent[len(m.sent)-1]
}

type countingSeats struct{ added int }

func (c *countingSeats) OnSeatAdded(context.Context, string) error   { c.added++; return nil }
func (c *countingSeats) OnSeatRemoved(context.Context, string) error { return nil }

type inviteEnv struct {
	t      *testing.T
	s      *sqlite.SQLiteStore
	h      *handlers.UserHandler
	org    *domain.Org
	owner  *domain.User
	admin  *domain.User
	seats  *countingSeats
	mailer *fakeInviteMailer
}

func newInviteEnv(t *testing.T, withMailer bool) *inviteEnv {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-inv", Name: "Invite Org", Slug: "invite-org", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	owner := &domain.User{ID: "owner-inv", OrgID: org.ID, Email: "owner@inv.test", Name: "Olive Owner", PasswordHash: "x", Role: domain.RoleOwner, CreatedAt: now, UpdatedAt: now}
	admin := &domain.User{ID: "admin-inv", OrgID: org.ID, Email: "admin@inv.test", PasswordHash: "x", Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now}
	for _, u := range []*domain.User{owner, admin} {
		if err := s.Users().Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	env := &inviteEnv{t: t, s: s, h: handlers.NewUserHandler(s), org: org, owner: owner, admin: admin, seats: &countingSeats{}}
	env.h.SetSeatUpdater(env.seats)
	if withMailer {
		env.mailer = &fakeInviteMailer{}
		env.h.SetInviteDelivery(env.mailer, "https://app.test/")
	} else {
		env.h.SetInviteDelivery(nil, "https://app.test")
	}
	return env
}

// router mounts the invite routes the way server.go does, with the caller's
// claims injected in place of the auth chain.
func (e *inviteEnv) router(caller *domain.User) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/invites/accept", e.h.GetInviteForAccept)
		r.Post("/invites/accept", e.h.AcceptInvite)
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{OrgID: caller.OrgID, UserID: caller.ID, Role: string(caller.Role)}
					next.ServeHTTP(w, req.WithContext(middleware.SetClaims(req.Context(), claims)))
				})
			})
			r.Use(middleware.RequireRole("owner", "admin"))
			r.Post("/users", e.h.LegacyCreate)
			r.Get("/users/invites", e.h.ListInvites)
			r.Post("/users/invites", e.h.CreateInvite)
			r.Post("/users/invites/{id}/resend", e.h.ResendInvite)
			r.Delete("/users/invites/{id}", e.h.RevokeInvite)
		})
	})
	return r
}

func (e *inviteEnv) do(caller *domain.User, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	if caller == nil {
		caller = e.owner
	}
	e.router(caller).ServeHTTP(rec, req)
	return rec
}

type deliveryResp struct {
	Invite struct {
		ID            string    `json:"id"`
		Email         string    `json:"email"`
		Role          string    `json:"role"`
		ExpiresAt     time.Time `json:"expires_at"`
		Expired       bool      `json:"expired"`
		InvitedByName string    `json:"invited_by_name"`
	} `json:"invite"`
	EmailSent bool   `json:"email_sent"`
	AcceptURL string `json:"accept_url"`
	Warning   string `json:"warning"`
}

func decodeDelivery(t *testing.T, rec *httptest.ResponseRecorder) deliveryResp {
	t.Helper()
	var d deliveryResp
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return d
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("no token in %q", link)
	}
	return tok
}

func tokenFromEmail(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "https://app.test/invite/accept?") {
			return tokenFromLink(t, line)
		}
	}
	t.Fatalf("no accept link in email:\n%s", body)
	return ""
}

// invite creates an invite without SMTP and returns its id and token.
func (e *inviteEnv) invite(email, role string) (string, string) {
	e.t.Helper()
	rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites", `{"email":"`+email+`","role":"`+role+`"}`)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("invite %s: %d %s", email, rec.Code, rec.Body.String())
	}
	d := decodeDelivery(e.t, rec)
	return d.Invite.ID, tokenFromLink(e.t, d.AcceptURL)
}

func acceptBody(token, password string) string {
	b, _ := json.Marshal(map[string]string{"token": token, "name": "New Person", "password": password})
	return string(b)
}

func TestInviteCreateEmailsLinkAndStoresOnlyHash(t *testing.T) {
	e := newInviteEnv(t, true)
	rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites", `{"email":"new@inv.test","role":"viewer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("response leaks a token or hash: %s", rec.Body.String())
	}
	d := decodeDelivery(t, rec)
	if !d.EmailSent || d.AcceptURL != "" {
		t.Fatalf("emailed invite must not return the link: %+v", d)
	}
	if d.Invite.Role != "viewer" || d.Invite.InvitedByName != "Olive Owner" {
		t.Fatalf("invite fields: %+v", d.Invite)
	}
	if got := time.Until(d.Invite.ExpiresAt); got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour {
		t.Fatalf("expiry %v, want 7 days", got)
	}
	msg := e.mailer.last(t)
	if msg.to != "new@inv.test" || !strings.Contains(msg.subject, "Invite Org") || !strings.Contains(msg.body, "Olive Owner invited you") {
		t.Fatalf("email: %+v", msg)
	}
	token := tokenFromEmail(t, msg.body)
	stored, err := e.s.Invites().GetByID(context.Background(), d.Invite.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, _ := strings.Cut(token, ".")
	sum := sha256.Sum256([]byte(secret))
	if stored.TokenHash != hex.EncodeToString(sum[:]) || strings.Contains(stored.TokenHash, secret) {
		t.Fatal("stored value is not the SHA-256 of the link secret")
	}
}

func TestInviteCreateWithoutSMTPReturnsLink(t *testing.T) {
	e := newInviteEnv(t, false)
	rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites", `{"email":"link@inv.test"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDelivery(t, rec)
	if d.EmailSent || !strings.HasPrefix(d.AcceptURL, "https://app.test/invite/accept?token=") || d.Invite.Role != "member" {
		t.Fatalf("no-SMTP invite: %+v", d)
	}
}

func TestInviteCreateValidation(t *testing.T) {
	e := newInviteEnv(t, false)
	cases := []struct {
		name   string
		caller *domain.User
		body   string
		want   int
	}{
		{"owner role", e.owner, `{"email":"a@inv.test","role":"owner"}`, http.StatusBadRequest},
		{"unknown role", e.owner, `{"email":"a@inv.test","role":"root"}`, http.StatusBadRequest},
		{"admin by admin", e.admin, `{"email":"a@inv.test","role":"admin"}`, http.StatusForbidden},
		{"bad email", e.owner, `{"email":"not an email","role":"member"}`, http.StatusBadRequest},
		{"named email", e.owner, `{"email":"Bob <b@inv.test>","role":"member"}`, http.StatusBadRequest},
		{"no email", e.owner, `{"role":"member"}`, http.StatusBadRequest},
		{"existing member", e.owner, `{"email":"admin@inv.test","role":"member"}`, http.StatusConflict},
		{"viewer by admin", e.admin, `{"email":"v@inv.test","role":"viewer"}`, http.StatusCreated},
		{"admin by owner", e.owner, `{"email":"ad@inv.test","role":"admin"}`, http.StatusCreated},
		{"duplicate pending, other case", e.owner, `{"email":"V@inv.test","role":"member"}`, http.StatusConflict},
	}
	for _, c := range cases {
		rec := e.do(c.caller, http.MethodPost, "/api/v1/users/invites", c.body)
		if rec.Code != c.want {
			t.Errorf("%s: got %d %s, want %d", c.name, rec.Code, rec.Body.String(), c.want)
		}
	}
	member := &domain.User{ID: "m", OrgID: e.org.ID, Role: domain.RoleMember}
	if rec := e.do(member, http.MethodPost, "/api/v1/users/invites", `{"email":"x@inv.test"}`); rec.Code != http.StatusForbidden {
		t.Errorf("member invite: got %d", rec.Code)
	}
}

func TestLegacyCreateWithPasswordIsGone(t *testing.T) {
	e := newInviteEnv(t, false)
	rec := e.do(e.owner, http.MethodPost, "/api/v1/users", `{"email":"p@inv.test","password":"chosenbyadmin"}`)
	if rec.Code != http.StatusGone {
		t.Fatalf("POST /users: got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.s.Users().GetByEmail(context.Background(), "p@inv.test"); err == nil {
		t.Fatal("legacy endpoint still created a user")
	}
}

func TestInviteListResendRevoke(t *testing.T) {
	e := newInviteEnv(t, false)
	ctx := context.Background()
	id1, oldToken := e.invite("one@inv.test", "member")
	id2, _ := e.invite("two@inv.test", "viewer")

	rec := e.do(e.admin, http.MethodGet, "/api/v1/users/invites", "")
	var list []struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		Expired       bool   `json:"expired"`
		InvitedByName string `json:"invited_by_name"`
		TokenHash     string `json:"token_hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %s", rec.Body.String())
	}
	for _, it := range list {
		if it.InvitedByName != "Olive Owner" || it.TokenHash != "" || it.Expired {
			t.Fatalf("list item: %+v", it)
		}
	}

	// Let invite 1 expire, then resend: new link, fresh 7-day window, and
	// the old link stops working.
	inv, _ := e.s.Invites().GetByID(ctx, id1)
	if err := e.s.Invites().Rotate(ctx, id1, inv.TokenHash, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec = e.do(e.admin, http.MethodGet, "/api/v1/users/invites", "")
	if !strings.Contains(rec.Body.String(), `"expired":true`) {
		t.Fatalf("expired invite not flagged: %s", rec.Body.String())
	}
	rec = e.do(e.admin, http.MethodPost, "/api/v1/users/invites/"+id1+"/resend", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDelivery(t, rec)
	newToken := tokenFromLink(t, d.AcceptURL)
	if newToken == oldToken || d.Invite.Expired || time.Until(d.Invite.ExpiresAt) < 7*24*time.Hour-time.Minute {
		t.Fatalf("resend did not rotate and reset: %+v", d)
	}
	if rec := e.do(nil, http.MethodGet, "/api/v1/invites/accept?token="+url.QueryEscape(oldToken), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("old token after resend: %d", rec.Code)
	}
	rec = e.do(nil, http.MethodGet, "/api/v1/invites/accept?token="+url.QueryEscape(newToken), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"org_name":"Invite Org"`) {
		t.Fatalf("new token: %d %s", rec.Code, rec.Body.String())
	}

	// Revoke invite 2; it leaves the list and cannot be revoked again.
	if rec := e.do(e.admin, http.MethodDelete, "/api/v1/users/invites/"+id2, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.admin, http.MethodDelete, "/api/v1/users/invites/"+id2, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second revoke: %d", rec.Code)
	}
	if rec := e.do(e.admin, http.MethodPost, "/api/v1/users/invites/"+id2+"/resend", ""); rec.Code != http.StatusConflict {
		t.Fatalf("resend revoked: %d", rec.Code)
	}
	rec = e.do(e.admin, http.MethodGet, "/api/v1/users/invites", "")
	if strings.Contains(rec.Body.String(), id2) {
		t.Fatalf("revoked invite still listed: %s", rec.Body.String())
	}

	// Another org cannot see or touch these invites.
	other := &domain.User{ID: "o", OrgID: "org-other", Role: domain.RoleOwner}
	if rec := e.do(other, http.MethodPost, "/api/v1/users/invites/"+id1+"/resend", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org resend: %d", rec.Code)
	}
	if rec := e.do(other, http.MethodDelete, "/api/v1/users/invites/"+id1, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org revoke: %d", rec.Code)
	}
}

func TestInviteAdminResendNeedsOwner(t *testing.T) {
	e := newInviteEnv(t, false)
	id, _ := e.invite("boss@inv.test", "admin")
	if rec := e.do(e.admin, http.MethodPost, "/api/v1/users/invites/"+id+"/resend", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin resending an admin invite: %d", rec.Code)
	}
	if rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites/"+id+"/resend", ""); rec.Code != http.StatusOK {
		t.Fatalf("owner resend: %d", rec.Code)
	}
}

func TestInviteAcceptCreatesUserOnce(t *testing.T) {
	e := newInviteEnv(t, true)
	ctx := context.Background()
	rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites", `{"email":"Joiner@inv.test","role":"viewer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	token := tokenFromEmail(t, e.mailer.last(t).body)

	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "short")); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d", rec.Code)
	}
	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, strings.Repeat("a", 73))); rec.Code != http.StatusBadRequest {
		t.Fatalf("long password: %d", rec.Code)
	}
	id, _, _ := strings.Cut(token, ".")
	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(id+".wrongsecret", "a-good-password")); rec.Code != http.StatusNotFound {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	if e.seats.added != 0 {
		t.Fatal("seat synced before any accept")
	}

	rec = e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "a-good-password"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	u, err := e.s.Users().GetByEmail(ctx, "Joiner@inv.test")
	if err != nil {
		t.Fatal(err)
	}
	if u.OrgID != e.org.ID || u.Role != domain.RoleViewer || u.Name != "New Person" || u.EmailVerifiedAt == nil || u.ForcePasswordChange {
		t.Fatalf("created user: %+v", u)
	}
	if err := auth.VerifyPassword(u.PasswordHash, "a-good-password"); err != nil {
		t.Fatal("user's own password not set")
	}
	if e.seats.added != 1 {
		t.Fatalf("seat sync calls = %d, want 1", e.seats.added)
	}

	// The same link cannot be used again.
	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "another-password")); rec.Code != http.StatusGone {
		t.Fatalf("reused token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(nil, http.MethodGet, "/api/v1/invites/accept?token="+url.QueryEscape(token), ""); rec.Code != http.StatusGone {
		t.Fatalf("reused token GET: %d", rec.Code)
	}
	if e.seats.added != 1 {
		t.Fatal("seat synced for a rejected accept")
	}
}

func TestInviteAcceptRejectsExpiredAndRevoked(t *testing.T) {
	e := newInviteEnv(t, false)
	ctx := context.Background()

	expID, expToken := e.invite("late@inv.test", "member")
	inv, _ := e.s.Invites().GetByID(ctx, expID)
	if err := e.s.Invites().Rotate(ctx, expID, inv.TokenHash, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(expToken, "a-good-password")); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("expired: %d %s", rec.Code, rec.Body.String())
	}

	revID, revToken := e.invite("gone@inv.test", "member")
	if rec := e.do(e.owner, http.MethodDelete, "/api/v1/users/invites/"+revID, ""); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(revToken, "a-good-password")); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "revoked") {
		t.Fatalf("revoked: %d %s", rec.Code, rec.Body.String())
	}
	for _, email := range []string{"late@inv.test", "gone@inv.test"} {
		if _, err := e.s.Users().GetByEmail(ctx, email); err == nil {
			t.Fatalf("user %s created from a dead invite", email)
		}
	}
	if rec := e.do(nil, http.MethodGet, "/api/v1/invites/accept?token=garbage", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("garbage token: %d", rec.Code)
	}
	if e.seats.added != 0 {
		t.Fatal("seat synced for a rejected accept")
	}
}

func TestInviteAcceptExistingAccount(t *testing.T) {
	e := newInviteEnv(t, false)
	ctx := context.Background()
	_, token := e.invite("dual@inv.test", "member")
	// The address registered elsewhere after the invite went out.
	now := time.Now().UTC()
	otherOrg := &domain.Org{ID: "org-2", Name: "Other", Slug: "other", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := e.s.Orgs().Create(ctx, otherOrg); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Users().Create(ctx, &domain.User{ID: "dual", OrgID: otherOrg.ID, Email: "dual@inv.test", PasswordHash: "x", Role: domain.RoleOwner, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(nil, http.MethodGet, "/api/v1/invites/accept?token="+url.QueryEscape(token), "")
	if !strings.Contains(rec.Body.String(), `"account_exists":true`) {
		t.Fatalf("GET does not flag the existing account: %s", rec.Body.String())
	}
	rec = e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "a-good-password"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"account_exists"`) {
		t.Fatalf("existing account: %d %s", rec.Code, rec.Body.String())
	}
	u, _ := e.s.Users().GetByEmail(ctx, "dual@inv.test")
	if u.OrgID != otherOrg.ID {
		t.Fatal("existing account was moved")
	}
}

func TestInviteFreePlanCountsPendingInvites(t *testing.T) {
	e := newInviteEnv(t, false)
	// The fixture org is on Free. Owner and admin are 2 of 5; three invites fill the plan.
	e.invite("f1@inv.test", "member")
	e.invite("f2@inv.test", "member")
	e.invite("f3@inv.test", "member")
	if rec := e.do(e.owner, http.MethodPost, "/api/v1/users/invites", `{"email":"f4@inv.test"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("fourth invite on Free: %d %s", rec.Code, rec.Body.String())
	}
}

// TestInviteAcceptSignsMemberIn covers the accept page contract: with a
// session starter wired (as server.go does) the response carries a session
// for the new member and sets the session cookie, so the page can go
// straight to the app.
func TestInviteAcceptSignsMemberIn(t *testing.T) {
	e := newInviteEnv(t, false)
	jwt := auth.NewJWTIssuer([]byte("invite-accept-test-secret"), time.Hour)
	authH := handlers.NewAuthHandlerWithBaseURL(e.s, jwt, "https://app.test")
	e.h.SetSessionStarter(authH.StartSession)

	_, token := e.invite("signin@inv.test", "member")
	rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "a-good-password"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		SignedIn bool   `json:"signed_in"`
		Token    string `json:"token"`
		User     struct {
			ID    string `json:"id"`
			OrgID string `json:"org_id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.SignedIn || resp.Token == "" || resp.User.Email != "signin@inv.test" || resp.User.Role != "member" || resp.User.OrgID != e.org.ID {
		t.Fatalf("accept response: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "a-good-password") || strings.Contains(rec.Body.String(), "$2a$") {
		t.Fatalf("response leaks password data: %s", rec.Body.String())
	}
	claims, err := jwt.Validate(resp.Token)
	if err != nil || claims.UserID != resp.User.ID || claims.OrgID != e.org.ID || claims.Role != "member" {
		t.Fatalf("session token: %+v %v", claims, err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "yipyap_session" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != resp.Token || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("session cookie: %+v", cookie)
	}
}

// Without a session starter the invitee is told to sign in.
func TestInviteAcceptWithoutSessionStarter(t *testing.T) {
	e := newInviteEnv(t, false)
	_, token := e.invite("nosession@inv.test", "viewer")
	rec := e.do(nil, http.MethodPost, "/api/v1/invites/accept", acceptBody(token, "a-good-password"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"signed_in":false`) || strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("accept response: %s", rec.Body.String())
	}
}
