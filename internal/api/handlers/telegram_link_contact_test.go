package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func (e *telegramLinkEnv) addUser(org *domain.Org, role domain.UserRole) *domain.User {
	e.t.Helper()
	u := &domain.User{ID: uuid.New().String(), OrgID: org.ID, Email: uuid.New().String()[:8] + "@example.com",
		Role: role, CreatedAt: e.now, UpdatedAt: e.now}
	if err := e.store.Users().Create(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

// callAs is call with the claims of a specific user.
func (e *telegramLinkEnv) callAs(u *domain.User, role string, fn http.HandlerFunc, method, path string, body any, params map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetClaims(req.Context(), &auth.Claims{UserID: u.ID, OrgID: u.OrgID, Role: role})
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

func (e *telegramLinkEnv) contactCode(u *domain.User) telegramLinkCodeResponse {
	e.t.Helper()
	rec := e.callAs(u, string(u.Role), e.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes",
		map[string]any{"purpose": "contact_method"}, nil)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create contact code: %d %s", rec.Code, rec.Body.String())
	}
	var resp telegramLinkCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *telegramLinkEnv) contactStatus(u *domain.User, id string) (int, telegramLinkCodeResponse) {
	e.t.Helper()
	rec := e.callAs(u, string(u.Role), e.chans.GetTelegramLinkCode, http.MethodGet, "/telegram/link-codes/"+id,
		nil, map[string]string{"id": id})
	var resp telegramLinkCodeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func (e *telegramLinkEnv) methodsOf(u *domain.User) []*domain.ContactMethod {
	e.t.Helper()
	list, err := e.store.(store.ContactMethodProvider).ContactMethods().ListByUser(context.Background(), u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

func (e *telegramLinkEnv) addMethod(u *domain.User, typ domain.ContactMethodType, cfg string, verified bool) *domain.ContactMethod {
	e.t.Helper()
	m := &domain.ContactMethod{ID: uuid.New().String(), OrgID: u.OrgID, UserID: u.ID, Type: typ, Label: string(typ),
		Config: cfg, CreatedAt: e.now, UpdatedAt: e.now}
	if verified {
		at := e.now
		m.VerifiedAt = &at
	}
	if err := e.store.(store.ContactMethodProvider).ContactMethods().Create(context.Background(), m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func privateChat(id int64, username string) map[string]any {
	return map[string]any{"id": id, "type": "private", "username": username, "first_name": "Jane"}
}

func TestTelegramContactCodeByMember(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)

	code := env.contactCode(member)
	if len(code.Code) != telegramLinkCodeLen || code.Command != "/link@YipYapBot "+code.Code || code.Status != "pending" {
		t.Fatalf("code = %+v", code)
	}
	if code.PrivateLink != "https://t.me/YipYapBot?start="+code.Code || code.GroupLink != "" {
		t.Fatalf("links = %q %q", code.PrivateLink, code.GroupLink)
	}
	if !code.ExpiresAt.Equal(env.now.Add(telegramLinkCodeTTL)) {
		t.Fatalf("expires_at = %v", code.ExpiresAt)
	}
	row, err := telegramLinkCodes(env.store).GetByID(context.Background(), code.ID)
	if err != nil || row.SubjectID != member.ID || row.CreatedBy != member.ID || row.OrgID != env.orgA.ID ||
		row.Purpose != domain.TelegramLinkPurposeContactMethod {
		t.Fatalf("stored code = %+v (%v)", row, err)
	}

	// Channel codes still follow the org's channel permissions.
	rec := env.callAs(member, "member", env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes",
		map[string]any{"purpose": "channel", "name": "Ops"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member channel code: %d %s", rec.Code, rec.Body.String())
	}
	// API keys cannot make personal codes.
	rec = env.callAs(member, "api_key", env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes",
		map[string]any{"purpose": "contact_method"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("api key contact code: %d %s", rec.Code, rec.Body.String())
	}

	// Without the platform bot there is nothing to link with.
	env.chans.SetTelegramBot("", false)
	rec = env.callAs(member, "member", env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes",
		map[string]any{"purpose": "contact_method"}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("linking disabled: %d", rec.Code)
	}
}

func TestTelegramContactLinkFromPrivateChat(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)
	other := env.addUser(env.orgA, domain.RoleAdmin)
	code := env.contactCode(member)

	reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+code.Code)
	if !strings.Contains(reply, "Connected") || !strings.Contains(reply, "Acme") || !strings.Contains(reply, "Contact Methods") ||
		!strings.Contains(reply, "notification rule") {
		t.Fatalf("reply = %q", reply)
	}
	methods := env.methodsOf(member)
	if len(methods) != 1 {
		t.Fatalf("methods = %d", len(methods))
	}
	m := methods[0]
	var cfg map[string]any
	_ = json.Unmarshal([]byte(m.Config), &cfg)
	if m.Type != domain.ContactTelegram || !m.Verified() || m.OrgID != env.orgA.ID || m.Label != "Telegram (@jane)" ||
		cfg["chat_id"] != "4242" || cfg["via_channel_id"] != nil {
		t.Fatalf("method = %+v cfg %v", m, cfg)
	}

	status, got := env.contactStatus(member, code.ID)
	if status != http.StatusOK || got.Status != "linked" || got.ContactMethodID != m.ID || got.ChannelID != "" ||
		got.ChatTitle != "@jane" || got.Code != "" {
		t.Fatalf("status = %d %+v", status, got)
	}
	// Nobody else can follow a personal code, not even an admin.
	if status, _ := env.contactStatus(other, code.ID); status != http.StatusNotFound {
		t.Fatalf("admin read another user's code: %d", status)
	}
	// The code works once.
	if reply := env.sendFromChat(privateChat(5151, "mallory"), "/start "+code.Code); !strings.Contains(reply, "already used") {
		t.Fatalf("reuse reply = %q", reply)
	}
	if n := len(env.methodsOf(member)); n != 1 {
		t.Fatalf("methods after reuse = %d", n)
	}
	// No channel came out of it.
	if n := len(env.channelsOf(env.orgA)); n != 0 {
		t.Fatalf("channels = %d", n)
	}
}

func TestTelegramContactLinkRefusedInGroup(t *testing.T) {
	for _, typ := range []string{"group", "supergroup", "channel"} {
		env := newTelegramLinkEnv(t)
		member := env.addUser(env.orgA, domain.RoleMember)
		code := env.contactCode(member)

		reply := env.sendMessage(-1001, typ, "Ops Room", code.Command)
		if reply != telegramContactPrivateOnlyReply {
			t.Fatalf("%s reply = %q", typ, reply)
		}
		if n := len(env.methodsOf(member)); n != 0 {
			t.Fatalf("%s: methods = %d", typ, n)
		}
		// Everyone in the group saw the code, so it is burned: otherwise any
		// member could send it privately and link their own Telegram to this
		// YipYap user.
		row, _ := telegramLinkCodes(env.store).GetByID(context.Background(), code.ID)
		if row.UsedAt == nil {
			t.Fatalf("%s: group attempt left the code usable", typ)
		}
		// After the bind grace the page shows failed, so the user makes a new code.
		env.now = env.now.Add(telegramLinkBindGrace + time.Second)
		if _, got := env.contactStatus(member, code.ID); got.Status != "failed" {
			t.Fatalf("%s: status = %+v, want failed", typ, got)
		}
		if reply := env.sendFromChat(privateChat(4242, "mallory"), code.Command); strings.Contains(reply, "Connected") {
			t.Fatalf("%s: burned code still linked: %q", typ, reply)
		}
		if n := len(env.methodsOf(member)); n != 0 {
			t.Fatalf("%s: methods after private retry = %d", typ, n)
		}
	}
}

func TestTelegramContactLinkDoesNotDuplicate(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)
	// An unverified platform bot method for the same chat, typed in by hand.
	existing := env.addMethod(member, domain.ContactTelegram, `{"chat_id":"4242"}`, false)
	// A method on the org's own bot is a different route, so it is left alone.
	own := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: env.orgA.ID, Type: "telegram", Name: "Own bot",
		Config: `{"bot_token":"123:abc"}`, Enabled: true}
	if err := env.store.NotificationChannels().Create(context.Background(), own); err != nil {
		t.Fatal(err)
	}
	viaOwn := env.addMethod(member, domain.ContactTelegram, fmt.Sprintf(`{"chat_id":"4242","via_channel_id":%q}`, own.ID), false)

	code := env.contactCode(member)
	reply := env.sendFromChat(privateChat(4242, "jane"), "/link "+code.Code)
	if !strings.Contains(reply, "is linked to your YipYap account") || !strings.Contains(reply, "Contact Methods") {
		t.Fatalf("reply = %q", reply)
	}
	methods := env.methodsOf(member)
	if len(methods) != 2 {
		t.Fatalf("methods = %d", len(methods))
	}
	for _, m := range methods {
		switch m.ID {
		case existing.ID:
			if !m.Verified() {
				t.Fatal("existing method was not verified")
			}
		case viaOwn.ID:
			if m.Verified() {
				t.Fatal("own bot method was verified by the platform bot")
			}
		}
	}
	if _, got := env.contactStatus(member, code.ID); got.Status != "linked" || got.ContactMethodID != existing.ID {
		t.Fatalf("status = %+v", got)
	}

	// Linking the same chat again keeps one method.
	again := env.contactCode(member)
	if reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+again.Code); !strings.Contains(reply, "is linked") {
		t.Fatalf("second reply = %q", reply)
	}
	if n := len(env.methodsOf(member)); n != 2 {
		t.Fatalf("methods after second link = %d", n)
	}
	if _, got := env.contactStatus(member, again.ID); got.Status != "linked" || got.ContactMethodID != existing.ID {
		t.Fatalf("second status = %+v", got)
	}
}

func TestTelegramContactCodeExpiredOrUsed(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)
	code := env.contactCode(member)
	env.now = env.now.Add(telegramLinkCodeTTL)
	if reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+code.Code); !strings.Contains(reply, "expired") {
		t.Fatalf("expired reply = %q", reply)
	}
	if n := len(env.methodsOf(member)); n != 0 {
		t.Fatalf("methods = %d", n)
	}
	if _, got := env.contactStatus(member, code.ID); got.Status != "expired" {
		t.Fatalf("status = %+v", got)
	}

	used := env.contactCode(member)
	if reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+used.Code); !strings.Contains(reply, "Connected") {
		t.Fatalf("first reply = %q", reply)
	}
	if reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+used.Code); !strings.Contains(reply, "already used") {
		t.Fatalf("used reply = %q", reply)
	}
}

func TestTelegramContactLinkRespectsMethodLimit(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)
	for i := 0; i < contactmethod.MaxMethodsPerUser-1; i++ {
		env.addMethod(member, domain.ContactWebhook, `{"url":"https://example.com"}`, true)
	}
	code := env.contactCode(member)

	// The last slot is taken after the code was made.
	env.addMethod(member, domain.ContactWebhook, `{"url":"https://example.com"}`, true)
	rec := env.callAs(member, "member", env.chans.CreateTelegramLinkCode, http.MethodPost, "/telegram/link-codes",
		map[string]any{"purpose": "contact_method"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code at limit: %d %s", rec.Code, rec.Body.String())
	}

	reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+code.Code)
	if !strings.Contains(reply, "most allowed") {
		t.Fatalf("reply = %q", reply)
	}
	if n := len(env.methodsOf(member)); n != contactmethod.MaxMethodsPerUser {
		t.Fatalf("methods = %d", n)
	}
	env.now = env.now.Add(telegramLinkBindGrace)
	if _, got := env.contactStatus(member, code.ID); got.Status != "failed" || got.ContactMethodID != "" {
		t.Fatalf("status = %+v", got)
	}
}

// A stored code whose user left the org (or a tampered row) binds nothing.
func TestTelegramContactLinkUserGone(t *testing.T) {
	env := newTelegramLinkEnv(t)
	member := env.addUser(env.orgA, domain.RoleMember)
	raw := "GONEUSER23"
	if err := telegramLinkCodes(env.store).Create(context.Background(), &domain.TelegramLinkCode{
		ID: uuid.New().String(), CodeHash: hashTelegramLinkCode(raw), OrgID: env.orgB.ID, CreatedBy: member.ID,
		Purpose: domain.TelegramLinkPurposeContactMethod, SubjectID: member.ID,
		ExpiresAt: env.now.Add(time.Minute), CreatedAt: env.now,
	}); err != nil {
		t.Fatal(err)
	}
	if reply := env.sendFromChat(privateChat(4242, "jane"), "/start "+raw); !strings.Contains(reply, "no longer exists") {
		t.Fatalf("reply = %q", reply)
	}
	if n := len(env.methodsOf(member)); n != 0 {
		t.Fatalf("methods = %d", n)
	}
}

func TestTelegramStartReplyMentionsContactMethods(t *testing.T) {
	env := newTelegramLinkEnv(t)
	reply := env.sendFromChat(privateChat(4242, "jane"), "/start")
	for _, want := range []string{"Settings, Contact Methods", "Connect Telegram", "Settings, Channels", "/link"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/start reply %q lacks %q", reply, want)
		}
	}
	if strings.Contains(reply, "4242") {
		t.Fatalf("/start reply exposed the chat id: %q", reply)
	}
}
