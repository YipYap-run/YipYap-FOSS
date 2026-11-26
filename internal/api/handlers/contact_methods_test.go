package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type cmClient struct {
	t     *testing.T
	base  string
	token string
}

func (c cmClient) do(method, path string, body any) (int, map[string]any, []any) {
	c.t.Helper()
	resp := doJSONReq(c.t, method, c.base+"/api/v1"+path, c.token, body)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var arr []any
	if json.Unmarshal(raw, &obj) != nil {
		_ = json.Unmarshal(raw, &arr)
	}
	return resp.StatusCode, obj, arr
}

func cmLogin(t *testing.T, base, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.Token == "" {
		t.Fatalf("login %s: %d", email, resp.StatusCode)
	}
	return out.Token
}

type cmFixture struct {
	srv    *httptest.Server
	s      *sqlite.SQLiteStore
	sender *capturingTestSender
	owner  cmClient
	member cmClient
	other  cmClient // owner of a different org
}

// cmTestEnvelopeHex is the notification key of the contact method fixture.
// The SaaS build refuses to store contact method secrets without one.
var cmTestEnvelopeHex = strings.Repeat("22", 32)

func newCMFixture(t *testing.T) cmFixture {
	t.Helper()
	env, err := crypto.NewEnvelopeFromHex(cmTestEnvelopeHex)
	if err != nil {
		t.Fatal(err)
	}
	srv, s, sender := setupChannelTestServerWith(t, env)
	owner := cmClient{t, srv.URL, signupAndGetToken(t, srv, "CM Org", "cm-owner@test.com", "supersecret123")}
	other := cmClient{t, srv.URL, signupAndGetToken(t, srv, "CM Other", "cm-other@test.com", "supersecret123")}
	code, obj, _ := owner.do(http.MethodPost, "/users/invites", map[string]any{
		"email": "cm-member@test.com", "role": "member",
	})
	if code != http.StatusCreated {
		t.Fatalf("invite member: %d %v", code, obj)
	}
	acceptURL, _ := obj["accept_url"].(string)
	token := acceptURL[strings.Index(acceptURL, "token=")+len("token="):]
	acceptBody, _ := json.Marshal(map[string]string{"token": token, "name": "Member", "password": "supersecret123"})
	acceptResp, err := http.Post(srv.URL+"/api/v1/invites/accept", "application/json", bytes.NewReader(acceptBody))
	if err != nil {
		t.Fatal(err)
	}
	_ = acceptResp.Body.Close()
	if acceptResp.StatusCode != http.StatusCreated {
		t.Fatalf("accept invite: %d", acceptResp.StatusCode)
	}
	member := cmClient{t, srv.URL, cmLogin(t, srv.URL, "cm-member@test.com", "supersecret123")}
	return cmFixture{srv: srv, s: s, sender: sender, owner: owner, member: member, other: other}
}

var sixDigits = regexp.MustCompile(`\b[0-9]{6}\b`)

func (f cmFixture) lastCode(t *testing.T) string {
	t.Helper()
	f.sender.mu.Lock()
	defer f.sender.mu.Unlock()
	if !f.sender.seen {
		t.Fatal("nothing was sent")
	}
	code := sixDigits.FindString(f.sender.job.Message)
	if code == "" {
		t.Fatalf("no code in message %q", f.sender.job.Message)
	}
	return code
}

func TestContactMethodsSeededFromVerifiedEmail(t *testing.T) {
	f := newCMFixture(t)
	code, _, arr := f.member.do(http.MethodGet, "/users/me/contact-methods", nil)
	if code != http.StatusOK || len(arr) != 1 {
		t.Fatalf("list: %d %v", code, arr)
	}
	m := arr[0].(map[string]any)
	if m["type"] != "email" || m["verified"] != true {
		t.Fatalf("seeded method = %v", m)
	}
	if cfg := m["config"].(map[string]any); cfg["address"] != "cm-member@test.com" {
		t.Fatalf("seeded address = %v", cfg)
	}
	code, _, rules := f.member.do(http.MethodGet, "/users/me/notification-rules", nil)
	if code != http.StatusOK || len(rules) != 1 {
		t.Fatalf("rules: %d %v", code, rules)
	}
	r := rules[0].(map[string]any)
	if r["contact_method_id"] != m["id"] || r["delay_minutes"].(float64) != 0 || r["urgency"] != "any" {
		t.Fatalf("seeded rule = %v", r)
	}
}

func TestContactMethodVerifyFlowAndPaging(t *testing.T) {
	f := newCMFixture(t)
	code, m, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "email", "label": "Personal", "config": map[string]any{"address": "me@example.com"},
	})
	if code != http.StatusCreated || m["verified"] != false {
		t.Fatalf("create: %d %v", code, m)
	}
	id := m["id"].(string)

	// A rule on an unverified method is allowed but never paged.
	code, rule, _ := f.member.do(http.MethodPost, "/users/me/notification-rules", map[string]any{
		"contact_method_id": id, "delay_minutes": 5, "urgency": "high",
	})
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d %v", code, rule)
	}
	member, _ := f.s.Users().GetByEmail(context.Background(), "cm-member@test.com")
	targets, err := f.s.NotificationRules().ListPagingTargets(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if tg.Method.ID == id {
			t.Fatal("unverified method returned for paging")
		}
	}

	// Test sends are refused until verified.
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/test", nil); code != http.StatusConflict {
		t.Fatalf("test unverified: %d", code)
	}

	// Send a code. No platform mailer here, so it goes through the smtp provider.
	if code, body, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/send-code", nil); code != http.StatusOK {
		t.Fatalf("send-code: %d %v", code, body)
	}
	f.sender.mu.Lock()
	job := f.sender.job
	f.sender.mu.Unlock()
	if job.Channel != "smtp" || job.ContactMethodID != id || job.TargetUserID != member.ID {
		t.Fatalf("code job = %+v", job)
	}
	sent := f.lastCode(t)

	// A second send inside the cooldown is refused.
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/send-code", nil); code != http.StatusTooManyRequests {
		t.Fatalf("second send-code: %d", code)
	}

	wrong := "000000"
	if sent == wrong {
		wrong = "111111"
	}
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/verify", map[string]string{"code": wrong}); code != http.StatusBadRequest {
		t.Fatalf("wrong code: %d", code)
	}
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/verify", map[string]string{"code": "abc"}); code != http.StatusBadRequest {
		t.Fatalf("malformed code: %d", code)
	}
	code, v, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/verify", map[string]string{"code": sent})
	if code != http.StatusOK || v["verified"] != true {
		t.Fatalf("verify: %d %v", code, v)
	}
	targets, _ = f.s.NotificationRules().ListPagingTargets(context.Background(), member.ID)
	found := false
	for _, tg := range targets {
		if tg.Method.ID == id && tg.Rule.DelayMinutes == 5 && tg.Rule.Urgency == domain.UrgencyHigh {
			found = true
		}
	}
	if !found {
		t.Fatal("verified method not returned for paging")
	}

	// Changing the address puts it back to unverified.
	code, u, _ := f.member.do(http.MethodPatch, "/users/me/contact-methods/"+id, map[string]any{
		"config": map[string]any{"address": "new@example.com"},
	})
	if code != http.StatusOK || u["verified"] != false {
		t.Fatalf("update: %d %v", code, u)
	}
	// A label-only change keeps the state.
	code, u, _ = f.member.do(http.MethodPatch, "/users/me/contact-methods/"+id, map[string]any{"label": "Renamed"})
	if code != http.StatusOK || u["label"] != "Renamed" {
		t.Fatalf("rename: %d %v", code, u)
	}
}

func TestContactMethodVerifyAttemptLimit(t *testing.T) {
	f := newCMFixture(t)
	_, m, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "email", "config": map[string]any{"address": "limit@example.com"},
	})
	id := m["id"].(string)
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/send-code", nil); code != http.StatusOK {
		t.Fatalf("send-code: %d", code)
	}
	sent := f.lastCode(t)
	wrong := "000000"
	if sent == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/verify", map[string]string{"code": wrong}); code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code, _, _ := f.member.do(http.MethodPost, "/users/me/contact-methods/"+id+"/verify", map[string]string{"code": sent}); code != http.StatusTooManyRequests {
		t.Fatalf("right code after limit: %d, want 429", code)
	}
}

func TestContactMethodValidation(t *testing.T) {
	f := newCMFixture(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown type", map[string]any{"type": "carrier_pigeon", "config": map[string]any{}}},
		{"bad email", map[string]any{"type": "email", "config": map[string]any{"address": "not-an-email"}}},
		{"email with name", map[string]any{"type": "email", "config": map[string]any{"address": "Bob <bob@example.com>"}}},
		{"email header injection", map[string]any{"type": "email", "config": map[string]any{"address": "a@example.com\r\nBcc: x@example.com"}}},
		{"unknown config field", map[string]any{"type": "email", "config": map[string]any{"address": "a@example.com", "cc": "b@example.com"}}},
		{"missing config", map[string]any{"type": "email"}},
		{"ntfy bad topic", map[string]any{"type": "ntfy", "config": map[string]any{"topic": "a/b"}}},
		{"ntfy http server", map[string]any{"type": "ntfy", "config": map[string]any{"topic": "ok", "server_url": "http://ntfy.example.com"}}},
		{"ntfy private server", map[string]any{"type": "ntfy", "config": map[string]any{"topic": "ok", "server_url": "https://127.0.0.1"}}},
		{"pushover short key", map[string]any{"type": "pushover", "config": map[string]any{"user_key": "abc", "api_token": "def"}}},
		{"slack dm no integration", map[string]any{"type": "slack_dm", "config": map[string]any{"slack_user_id": "U0123ABCD"}}},
		{"slack dm bad member id", map[string]any{"type": "slack_dm", "config": map[string]any{"slack_user_id": "bob", "via_channel_id": "x"}}},
		{"discord dm bad id", map[string]any{"type": "discord_dm", "config": map[string]any{"dm_channel_id": "abc", "via_channel_id": "x"}}},
		{"label too long", map[string]any{"type": "email", "label": string(bytes.Repeat([]byte("a"), 101)), "config": map[string]any{"address": "a@example.com"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d %v", code, body)
			}
		})
	}

	// A valid ntfy method stores a redacted token.
	code, m, _ := f.member.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "ntfy", "config": map[string]any{"topic": "my-pages", "token": "tk_secret"},
	})
	if code != http.StatusCreated {
		t.Fatalf("ntfy create: %d %v", code, m)
	}
	if cfg := m["config"].(map[string]any); cfg["token"] == "tk_secret" {
		t.Fatal("ntfy token returned in clear")
	}

	// Rule validation.
	id := m["id"].(string)
	for _, body := range []map[string]any{
		{"contact_method_id": id, "delay_minutes": -1},
		{"contact_method_id": id, "delay_minutes": 1441},
		{"contact_method_id": id, "urgency": "whenever"},
		{"delay_minutes": 0},
	} {
		if code, out, _ := f.member.do(http.MethodPost, "/users/me/notification-rules", body); code != http.StatusBadRequest {
			t.Fatalf("rule %v: want 400, got %d %v", body, code, out)
		}
	}
}

// A user cannot see or change anyone else's methods or rules, in or out of
// their org.
func TestContactMethodCrossUserAccessDenied(t *testing.T) {
	f := newCMFixture(t)
	_, m, _ := f.owner.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "email", "config": map[string]any{"address": "owner-alt@example.com"},
	})
	id := m["id"].(string)
	code, _, arr := f.owner.do(http.MethodGet, "/users/me/notification-rules", nil)
	if code != http.StatusOK || len(arr) == 0 {
		t.Fatalf("owner rules: %d", code)
	}
	ruleID := arr[0].(map[string]any)["id"].(string)

	for _, c := range []cmClient{f.member, f.other} {
		for _, req := range []struct{ method, path string }{
			{http.MethodGet, "/users/me/contact-methods/" + id},
			{http.MethodPatch, "/users/me/contact-methods/" + id},
			{http.MethodDelete, "/users/me/contact-methods/" + id},
			{http.MethodPost, "/users/me/contact-methods/" + id + "/test"},
			{http.MethodPost, "/users/me/contact-methods/" + id + "/send-code"},
			{http.MethodPost, "/users/me/contact-methods/" + id + "/verify"},
			{http.MethodPatch, "/users/me/notification-rules/" + ruleID},
			{http.MethodDelete, "/users/me/notification-rules/" + ruleID},
		} {
			body := map[string]any{"label": "x", "code": "123456", "delay_minutes": 1}
			if code, out, _ := c.do(req.method, req.path, body); code != http.StatusNotFound {
				t.Fatalf("%s %s: want 404, got %d %v", req.method, req.path, code, out)
			}
		}
		// A rule cannot point at someone else's method.
		if code, _, _ := c.do(http.MethodPost, "/users/me/notification-rules", map[string]any{"contact_method_id": id}); code != http.StatusBadRequest {
			t.Fatalf("rule on foreign method: %d", code)
		}
		// Their own list never shows it.
		_, _, list := c.do(http.MethodGet, "/users/me/contact-methods", nil)
		for _, it := range list {
			if it.(map[string]any)["id"] == id {
				t.Fatal("foreign method listed")
			}
		}
	}
	if _, err := f.s.ContactMethods().Get(context.Background(), mustUserID(t, f.s, "cm-owner@test.com"), id); err != nil {
		t.Fatalf("owner's method was changed or removed: %v", err)
	}
}

func mustUserID(t *testing.T, s *sqlite.SQLiteStore, email string) string {
	t.Helper()
	u, err := s.Users().GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestAdminReadsMemberContactMethods(t *testing.T) {
	f := newCMFixture(t)
	memberID := mustUserID(t, f.s, "cm-member@test.com")
	ownerID := mustUserID(t, f.s, "cm-owner@test.com")

	code, out, _ := f.owner.do(http.MethodGet, "/users/"+memberID+"/contact-methods", nil)
	if code != http.StatusOK {
		t.Fatalf("admin read: %d %v", code, out)
	}
	methods := out["contact_methods"].([]any)
	if len(methods) != 1 {
		t.Fatalf("methods = %v", methods)
	}
	m := methods[0].(map[string]any)
	if _, has := m["config"]; has {
		t.Fatal("admin view exposes config")
	}
	if m["summary"] == "cm-member@test.com" || m["verified"] != true {
		t.Fatalf("admin summary not masked: %v", m)
	}
	if rules := out["notification_rules"].([]any); len(rules) != 1 {
		t.Fatalf("rules = %v", rules)
	}

	// Admin view is read-only: there is no write route for another user.
	if code, _, _ := f.owner.do(http.MethodPost, "/users/"+memberID+"/contact-methods", map[string]any{}); code != http.StatusMethodNotAllowed && code != http.StatusNotFound {
		t.Fatalf("admin write: %d", code)
	}
	// Members cannot read other people's methods.
	if code, _, _ := f.member.do(http.MethodGet, "/users/"+ownerID+"/contact-methods", nil); code != http.StatusForbidden {
		t.Fatalf("member read: %d", code)
	}
	// Admins of another org cannot read them either.
	if code, _, _ := f.other.do(http.MethodGet, "/users/"+memberID+"/contact-methods", nil); code != http.StatusNotFound {
		t.Fatalf("cross-org admin read: %d", code)
	}
}

func TestContactMethodsRejectAPIKeys(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := handlers.NewContactMethodHandler(s, nil, nil, contactmethod.Cipher{})
	req := httptest.NewRequest(http.MethodGet, "/users/me/contact-methods", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: "u", OrgID: "o", Role: "api_key"}))
	rec := httptest.NewRecorder()
	h.ListMethods(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("api key list: %d", rec.Code)
	}
}
