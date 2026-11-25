package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

const acctTestPassword = "a long enough password"

type sentMail struct{ to, subject, body string }

// mailbox records mail sent through a handler's sendMail hook.
type mailbox struct {
	mu   sync.Mutex
	msgs []sentMail
}

func (m *mailbox) send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, sentMail{to, subject, body})
	return nil
}

// waitFor returns the messages once at least n were sent, or fails.
func (m *mailbox) waitFor(t *testing.T, n int) []sentMail {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		got := append([]sentMail(nil), m.msgs...)
		m.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d emails, got %d: %+v", n, len(got), got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (m *mailbox) to(t *testing.T, n int, addr string) sentMail {
	t.Helper()
	for _, msg := range m.waitFor(t, n) {
		if msg.to == addr {
			return msg
		}
	}
	t.Fatalf("no email to %s", addr)
	return sentMail{}
}

var linkTokenRe = regexp.MustCompile(`token=([A-Za-z0-9._-]+)`)

func linkToken(t *testing.T, body string) string {
	t.Helper()
	m := linkTokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token link in email body: %q", body)
	}
	return m[1]
}

type acctEnv struct {
	store *sqlite.SQLiteStore
	jwt   *auth.JWTIssuer
	mail  *mailbox
	// auth has a mailer; plain has none (self-hosted without SMTP).
	auth  *AuthHandler
	plain *AuthHandler
	reset *PasswordResetHandler
}

func newAcctEnv(t *testing.T) *acctEnv {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	jwt := auth.NewJWTIssuer([]byte("account-email-test-secret"), time.Hour)
	m := mailer.New(mailer.Config{Host: "mail.invalid"})
	box := &mailbox{}
	a := NewAuthHandlerFull(s, jwt, m, "https://app.test")
	a.sendMail = box.send
	r := NewPasswordResetHandler(s, jwt, m, "https://app.test")
	r.sendMail = box.send
	return &acctEnv{store: s, jwt: jwt, mail: box, auth: a, plain: NewAuthHandler(s, jwt), reset: r}
}

// createUser makes a verified user with acctTestPassword in a new org.
func (e *acctEnv) createUser(t *testing.T, email string, role domain.UserRole) *domain.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: uuid.New().String(), Name: "Org", Slug: "org-" + uuid.New().String()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := e.store.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return e.createUserIn(t, org.ID, email, role)
}

// createUserIn makes a verified user with acctTestPassword in orgID.
func (e *acctEnv) createUserIn(t *testing.T, orgID, email string, role domain.UserRole) *domain.User {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	hash, err := auth.HashPassword(acctTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: uuid.New().String(), OrgID: orgID, Email: email, PasswordHash: hash, Role: role, CreatedAt: now, UpdatedAt: now}
	if err := e.store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Users().MarkEmailVerified(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.Users().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func post(h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// asSession runs h as u with a real server-side session.
func (e *acctEnv) asSession(t *testing.T, u *domain.User, method string, body any, h http.HandlerFunc, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/", nil)
	tok, err := session.Issue(context.Background(), e.store, e.jwt, req, u.ID, u.OrgID, string(u.Role), auth.AMRPassword)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := e.jwt.Validate(tok)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	req = httptest.NewRequest(method, "/", bytes.NewReader(b))
	ctx := middleware.SetClaims(req.Context(), claims)
	if len(params) > 0 {
		rc := chi.NewRouteContext()
		for k, v := range params {
			rc.URLParams.Add(k, v)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	}
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	return rec
}

func acctBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

// auth-8: an address typed with different case or spaces signs in and gets
// a reset email.
func TestEmailNormalizedForLoginAndReset(t *testing.T) {
	e := newAcctEnv(t)

	rec := post(e.plain.Register, registerRequest{OrgName: "Case Co", Email: "  Mixed.Case@Example.COM ", Password: acctTestPassword})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.Users().GetByEmail(context.Background(), "mixed.case@example.com"); err != nil {
		t.Fatalf("stored email is not normalized: %v", err)
	}

	for _, typed := range []string{"mixed.case@example.com", "MIXED.CASE@EXAMPLE.COM", " Mixed.Case@example.com"} {
		rec = post(e.plain.Login, loginRequest{Email: typed, Password: acctTestPassword})
		if rec.Code != http.StatusOK {
			t.Fatalf("login as %q: %d %s", typed, rec.Code, rec.Body)
		}
	}

	rec = post(e.reset.ForgotPassword, map[string]string{"email": "MIXED.Case@Example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot password: %d", rec.Code)
	}
	msg := e.mail.to(t, 1, "mixed.case@example.com")
	tok := linkToken(t, msg.body)
	rec = post(e.reset.ResetPassword, map[string]string{"token": tok, "password": "another long password"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset with mailed token: %d %s", rec.Code, rec.Body)
	}
}

// auth-8: the store matches without case and refuses a second account that
// differs only by case.
func TestUserStoreEmailCaseInsensitive(t *testing.T) {
	e := newAcctEnv(t)
	u := e.createUser(t, "Owner@Example.com", domain.RoleOwner)
	if u.Email != "owner@example.com" {
		t.Fatalf("Create did not normalize: %q", u.Email)
	}
	if got, err := e.store.Users().GetByEmail(context.Background(), "OWNER@example.COM"); err != nil || got.ID != u.ID {
		t.Fatalf("GetByEmail mixed case: %v", err)
	}
	now := time.Now().UTC()
	dup := &domain.User{ID: uuid.New().String(), OrgID: u.OrgID, Email: "OWNER@EXAMPLE.COM", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := e.store.Users().Create(context.Background(), dup); err == nil {
		t.Fatal("a case-only duplicate was accepted")
	}
}

// auth-7: two orgs with the same name both register with distinct slugs; a
// genuine email conflict gets the 409 copy and code the SPA links from.
func TestRegisterSlugAndEmailConflicts(t *testing.T) {
	e := newAcctEnv(t)
	ctx := context.Background()

	rec := post(e.plain.Register, registerRequest{OrgName: "Acme", Email: "a@acme.test", Password: acctTestPassword})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first register: %d %s", rec.Code, rec.Body)
	}
	rec = post(e.plain.Register, registerRequest{OrgName: "Acme", Email: "b@other.test", Password: acctTestPassword})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second org with the same name: %d %s", rec.Code, rec.Body)
	}
	a, _ := e.store.Users().GetByEmail(ctx, "a@acme.test")
	b, _ := e.store.Users().GetByEmail(ctx, "b@other.test")
	orgA, _ := e.store.Orgs().GetByID(ctx, a.OrgID)
	orgB, _ := e.store.Orgs().GetByID(ctx, b.OrgID)
	if orgA.Slug != "acme" || orgB.Slug == orgA.Slug || !strings.HasPrefix(orgB.Slug, "acme-") {
		t.Fatalf("slugs: %q and %q", orgA.Slug, orgB.Slug)
	}
	if orgB.Name != "Acme" {
		t.Fatalf("org name changed: %q", orgB.Name)
	}

	rec = post(e.plain.Register, registerRequest{OrgName: "Different", Email: "A@ACME.test", Password: acctTestPassword})
	if rec.Code != http.StatusConflict {
		t.Fatalf("email conflict: want 409, got %d %s", rec.Code, rec.Body)
	}
	m := acctBody(t, rec)
	if m["code"] != codeEmailTaken || m["error"] != emailTakenMessage {
		t.Fatalf("conflict body: %v", m)
	}
	if _, err := e.store.Orgs().GetBySlug(ctx, "different"); err == nil {
		t.Fatal("a conflicting sign-up created an org")
	}
}

// auth-13: the change waits for the link mailed to the new address, the old
// address is told, and the link works once.
func TestEmailChangeAppliesOnlyOnConfirmation(t *testing.T) {
	e := newAcctEnv(t)
	ctx := context.Background()
	u := e.createUser(t, "old@example.com", domain.RoleOwner)

	rec := e.asSession(t, u, http.MethodPut, map[string]string{
		"current_password": acctTestPassword, "new_email": " New@Example.com",
	}, e.auth.ChangeEmail, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("change email: want 202, got %d %s", rec.Code, rec.Body)
	}
	if m := acctBody(t, rec); m["status"] != "confirmation_sent" || m["pending_email"] != "new@example.com" {
		t.Fatalf("change email body: %v", m)
	}

	got, _ := e.store.Users().GetByID(ctx, u.ID)
	if got.Email != "old@example.com" || got.PendingEmail != "new@example.com" || got.PendingEmailRequestedAt == nil {
		t.Fatalf("before confirm: email=%q pending=%q", got.Email, got.PendingEmail)
	}
	if rec := post(e.plain.Login, loginRequest{Email: "new@example.com", Password: acctTestPassword}); rec.Code == http.StatusOK {
		t.Fatal("the unconfirmed address signs in")
	}

	confirm := e.mail.to(t, 2, "new@example.com")
	notice := e.mail.to(t, 2, "old@example.com")
	if strings.Contains(notice.body, "new@example.com") {
		t.Fatal("the notice to the old address shows the full new address")
	}
	if !strings.Contains(notice.body, "cancel-email-change?token=") {
		t.Fatal("the notice to the old address has no cancel link")
	}
	confirmTok := linkToken(t, confirm.body)
	if linkToken(t, notice.body) == confirmTok {
		t.Fatal("the cancel link reuses the confirmation token")
	}
	// The cancel token cannot confirm.
	if rec := post(e.auth.ConfirmEmailChange, map[string]string{"token": linkToken(t, notice.body)}); rec.Code == http.StatusOK {
		t.Fatal("a cancel token confirmed the change")
	}

	rec = post(e.auth.ConfirmEmailChange, map[string]string{"token": confirmTok})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	got, _ = e.store.Users().GetByID(ctx, u.ID)
	if got.Email != "new@example.com" || got.PendingEmail != "" || got.EmailVerifiedAt == nil {
		t.Fatalf("after confirm: email=%q pending=%q", got.Email, got.PendingEmail)
	}
	if rec := post(e.plain.Login, loginRequest{Email: "new@example.com", Password: acctTestPassword}); rec.Code != http.StatusOK {
		t.Fatalf("login with the confirmed address: %d", rec.Code)
	}
	// The old address hears that the change happened.
	msgs := e.mail.waitFor(t, 3)
	if msgs[2].to != "old@example.com" {
		t.Fatalf("completion notice went to %q", msgs[2].to)
	}

	if rec := post(e.auth.ConfirmEmailChange, map[string]string{"token": confirmTok}); rec.Code != http.StatusBadRequest {
		t.Fatalf("replayed confirm: want 400, got %d", rec.Code)
	}
}

func TestEmailChangeCancelAndSupersede(t *testing.T) {
	e := newAcctEnv(t)
	ctx := context.Background()
	u := e.createUser(t, "me@example.com", domain.RoleOwner)

	rec := e.asSession(t, u, http.MethodPut, map[string]string{
		"current_password": acctTestPassword, "new_email": "typo@exmaple.com",
	}, e.auth.ChangeEmail, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("change email: %d %s", rec.Code, rec.Body)
	}
	confirmTok := linkToken(t, e.mail.to(t, 2, "typo@exmaple.com").body)
	cancelTok := linkToken(t, e.mail.to(t, 2, "me@example.com").body)

	// A second request inside the cooldown is refused.
	rec = e.asSession(t, u, http.MethodPut, map[string]string{
		"current_password": acctTestPassword, "new_email": "other@example.com",
	}, e.auth.ChangeEmail, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request inside cooldown: want 429, got %d", rec.Code)
	}

	// The cancel link from the old address stops the change.
	if rec := post(e.auth.CancelEmailChange, map[string]string{"token": cancelTok}); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := post(e.auth.ConfirmEmailChange, map[string]string{"token": confirmTok}); rec.Code != http.StatusBadRequest {
		t.Fatalf("confirm after cancel: want 400, got %d", rec.Code)
	}
	got, _ := e.store.Users().GetByID(ctx, u.ID)
	if got.Email != "me@example.com" || got.PendingEmail != "" {
		t.Fatalf("after cancel: email=%q pending=%q", got.Email, got.PendingEmail)
	}

	// A newer request voids the links of the older one.
	if err := e.store.Users().SetPendingEmail(ctx, u.ID, "typo@exmaple.com", "first-nonce", time.Now()); err != nil {
		t.Fatal(err)
	}
	oldTok, err := e.jwt.IssueEmailChange(auth.EmailChangeConfirmAudience, u.ID, u.OrgID, "typo@exmaple.com", "first-nonce")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Users().SetPendingEmail(ctx, u.ID, "right@example.com", "second-nonce", time.Now()); err != nil {
		t.Fatal(err)
	}
	if rec := post(e.auth.ConfirmEmailChange, map[string]string{"token": oldTok}); rec.Code != http.StatusBadRequest {
		t.Fatalf("superseded link: want 400, got %d", rec.Code)
	}

	// Signed-in cancel.
	if rec := e.asSession(t, u, http.MethodDelete, nil, e.auth.CancelPendingEmail, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel pending: %d", rec.Code)
	}
	if rec := e.asSession(t, u, http.MethodDelete, nil, e.auth.CancelPendingEmail, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cancel with nothing pending: %d", rec.Code)
	}
}

func TestEmailChangeRefusesTakenAndInvalid(t *testing.T) {
	e := newAcctEnv(t)
	u := e.createUser(t, "me@example.com", domain.RoleOwner)
	e.createUser(t, "taken@example.com", domain.RoleOwner)

	cases := map[string]int{
		"TAKEN@example.com": http.StatusConflict,
		"ME@example.com":    http.StatusBadRequest,
		"not-an-email":      http.StatusBadRequest,
		"Jane <j@x.com>":    http.StatusBadRequest,
	}
	for addr, want := range cases {
		rec := e.asSession(t, u, http.MethodPut, map[string]string{
			"current_password": acctTestPassword, "new_email": addr,
		}, e.auth.ChangeEmail, nil)
		if rec.Code != want {
			t.Errorf("new_email %q: want %d, got %d %s", addr, want, rec.Code, rec.Body)
		}
	}

	// A wrong password never mails anything.
	rec := e.asSession(t, u, http.MethodPut, map[string]string{
		"current_password": "wrong password!", "new_email": "fresh@example.com",
	}, e.auth.ChangeEmail, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(e.mail.waitFor(t, 0)); n != 0 {
		t.Fatalf("%d emails sent for refused requests", n)
	}
}

// Without SMTP nothing can confirm the address, so the change applies at
// once and says so.
func TestEmailChangeWithoutMailerAppliesDirectly(t *testing.T) {
	e := newAcctEnv(t)
	u := e.createUser(t, "me@example.com", domain.RoleOwner)
	rec := e.asSession(t, u, http.MethodPut, map[string]string{
		"current_password": acctTestPassword, "new_email": "New@Example.com",
	}, e.plain.ChangeEmail, nil)
	if rec.Code != http.StatusOK || acctBody(t, rec)["status"] != "updated" {
		t.Fatalf("no-mailer change: %d %s", rec.Code, rec.Body)
	}
	got, _ := e.store.Users().GetByID(context.Background(), u.ID)
	if got.Email != "new@example.com" {
		t.Fatalf("email = %q", got.Email)
	}
}

// auth-13: one password policy on every path that sets a password.
func TestPasswordPolicyOnEveryPath(t *testing.T) {
	e := newAcctEnv(t)
	owner := e.createUser(t, "owner@example.com", domain.RoleOwner)
	member := e.createUserIn(t, owner.OrgID, "member@example.com", domain.RoleMember)
	users := NewUserHandler(e.store)

	tooLong := strings.Repeat("x", auth.MaxPasswordBytes+1)
	for _, bad := range []string{"short", "", tooLong, strings.Repeat("é", 37)} {
		resetTok, err := e.jwt.IssuePasswordReset(owner.ID, owner.OrgID, owner.Email, owner.PasswordHash)
		if err != nil {
			t.Fatal(err)
		}
		paths := map[string]*httptest.ResponseRecorder{
			"register":    post(e.plain.Register, registerRequest{OrgName: "P", Email: "p@example.com", Password: bad}),
			"change":      e.asSession(t, owner, http.MethodPut, map[string]string{"current_password": acctTestPassword, "new_password": bad}, e.plain.ChangePassword, nil),
			"reset":       post(e.reset.ResetPassword, map[string]string{"token": resetTok, "password": bad}),
			"admin reset": e.asSession(t, owner, http.MethodPost, map[string]string{"temporary_password": bad}, users.ResetPassword, map[string]string{"id": member.ID}),
		}
		for path, rec := range paths {
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with password of %d bytes: want 400, got %d %s", path, len(bad), rec.Code, rec.Body)
			}
		}
	}

	// A password at the limits passes the policy.
	for _, ok := range []string{"12345678", strings.Repeat("x", auth.MaxPasswordBytes), "ééééééééé"} {
		if err := auth.ValidatePassword(ok); err != nil {
			t.Errorf("ValidatePassword(%d bytes): %v", len(ok), err)
		}
	}
}

// Invites normalize the address and refuse a case-only duplicate; an admin
// cannot change another member's email from the member editor.
func TestAdminUserEmailNormalized(t *testing.T) {
	e := newAcctEnv(t)
	owner := e.createUser(t, "owner@example.com", domain.RoleOwner)
	users := NewUserHandler(e.store)

	rec := e.asSession(t, owner, http.MethodPost, map[string]any{"email": " Member@Example.com", "role": "member"}, users.CreateInvite, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Invite struct {
			Email string `json:"email"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	if out.Invite.Email != "member@example.com" {
		t.Fatalf("invite did not normalize: %s", rec.Body)
	}
	rec = e.asSession(t, owner, http.MethodPost, map[string]any{"email": "MEMBER@example.com", "role": "member"}, users.CreateInvite, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("case-only duplicate invite: want 409, got %d %s", rec.Code, rec.Body)
	}

	member := e.createUserIn(t, owner.OrgID, "member@example.com", domain.RoleMember)
	if rec := e.asSession(t, owner, http.MethodPatch, map[string]any{"email": "OWNER@example.com"}, users.Update, map[string]string{"id": member.ID}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin changing another user's email: want 403, got %d %s", rec.Code, rec.Body)
	}
}

// The SPA branches on these codes to show its own copy and the resend wait
// (web/src/utils/email-change.js), so they are part of the API contract.
func TestEmailChangeErrorCodesForUI(t *testing.T) {
	e := newAcctEnv(t)
	u := e.createUser(t, "me@example.com", domain.RoleOwner)
	e.createUser(t, "taken@example.com", domain.RoleOwner)

	change := func(addr string) *httptest.ResponseRecorder {
		return e.asSession(t, u, http.MethodPut, map[string]string{
			"current_password": acctTestPassword, "new_email": addr,
		}, e.auth.ChangeEmail, nil)
	}
	for addr, want := range map[string]string{
		"Me@Example.com":    "email_unchanged",
		"taken@example.com": "email_taken",
		"not-an-email":      "invalid_email",
	} {
		if got := acctBody(t, change(addr))["code"]; got != want {
			t.Errorf("new_email %q: code = %v, want %s", addr, got, want)
		}
	}

	if rec := change("first@example.com"); rec.Code != http.StatusAccepted {
		t.Fatalf("first request: %d %s", rec.Code, rec.Body)
	}
	rec := change("second@example.com")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("inside cooldown: want 429, got %d", rec.Code)
	}
	body := acctBody(t, rec)
	if body["code"] != "email_change_cooldown" {
		t.Errorf("cooldown code = %v", body["code"])
	}
	if n, ok := body["retry_after"].(float64); !ok || n < 1 || n > 60 {
		t.Errorf("retry_after = %v, want 1..60", body["retry_after"])
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}

	// The user JSON carries the pending fields the Account notice reads.
	got, err := e.store.Users().GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["pending_email"] != "first@example.com" || m["pending_email_requested_at"] == nil {
		t.Errorf("user JSON pending fields: %v / %v", m["pending_email"], m["pending_email_requested_at"])
	}
	if _, leaked := m["pending_email_nonce"]; leaked {
		t.Error("pending email nonce must not be serialized")
	}

	if code := acctBody(t, post(e.auth.ConfirmEmailChange, map[string]string{"token": "bogus"}))["code"]; code != "email_change_link_invalid" {
		t.Errorf("bad link code = %v", code)
	}
}
