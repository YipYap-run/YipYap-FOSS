package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// setupEmailChannelServer starts an API server. withPlatform controls
// whether a platform mailer is configured.
func setupEmailChannelServer(t *testing.T, withPlatform bool) (*httptest.Server, *sqlite.SQLiteStore, *capturingTestSender, string) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ts := &capturingTestSender{}
	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)

	// With a mailer configured, registration waits for email verification
	// and returns no token, so register through a mailer-less server that
	// shares the store and signing key.
	regSrv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{RegistrationEnabled: true}))
	token := ceRegisterAndGetToken(t, regSrv, "EmailOrg", "owner@email.test", "supersecret123")
	regSrv.Close()

	opts := api.ServerOptions{TestSender: handlers.TestSender(ts)}
	if withPlatform {
		opts.Mailer = mailer.New(mailer.Config{Host: "smtp.platform.example", From: "noreply@platform.example"})
	}
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", opts))
	t.Cleanup(srv.Close)
	return srv, s, ts, token
}

func fullSMTPConfig() map[string]interface{} {
	return map[string]interface{}{
		"mode":     "smtp",
		"host":     "example.com",
		"port":     587,
		"tls_mode": "starttls",
		"username": "alerts",
		"password": "org-secret",
		"from":     "alerts@org.example",
		"to":       "ops@org.example",
	}
}

func createEmailChannel(t *testing.T, srv *httptest.Server, token string, cfg map[string]interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels", token, map[string]interface{}{
		"name": "Email", "type": "smtp", "enabled": true, "config": cfg,
	})
	defer func() { _ = resp.Body.Close() }()
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func storedConfig(t *testing.T, s *sqlite.SQLiteStore, id string) map[string]interface{} {
	t.Helper()
	ch, err := s.NotificationChannels().GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(ch.Config), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEmailChannel_PlatformModeStoresRecipientsOnly(t *testing.T) {
	srv, s, _, token := setupEmailChannelServer(t, true)
	resp, body := createEmailChannel(t, srv, token, map[string]interface{}{"to": "a@org.example, b@org.example"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	got := storedConfig(t, s, body["id"].(string))
	want := map[string]interface{}{"mode": "platform", "to": "a@org.example, b@org.example"}
	if len(got) != len(want) || got["mode"] != want["mode"] || got["to"] != want["to"] {
		t.Fatalf("stored config = %v, want %v", got, want)
	}
}

func TestEmailChannel_PlatformModeRejectsServerFields(t *testing.T) {
	srv, _, _, token := setupEmailChannelServer(t, true)
	for _, k := range []string{"host", "port", "tls_mode", "username", "password", "from"} {
		cfg := map[string]interface{}{"mode": "platform", "to": "a@org.example", k: fullSMTPConfig()[k]}
		resp, body := createEmailChannel(t, srv, token, cfg)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", k, resp.StatusCode)
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, k+" ") {
			t.Errorf("%s: error %q does not name the field", k, msg)
		}
	}
	// A legacy-shaped body (no mode) that names a host is smtp mode and so
	// needs the full set of server fields.
	resp, _ := createEmailChannel(t, srv, token, map[string]interface{}{"host": "example.com", "port": 587, "to": "a@org.example"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("host without credentials: status = %d, want 400", resp.StatusCode)
	}
}

func TestEmailChannel_SMTPModeRequiresEveryField(t *testing.T) {
	srv, _, _, token := setupEmailChannelServer(t, true)
	for _, k := range []string{"host", "port", "tls_mode", "username", "password", "from", "to"} {
		cfg := fullSMTPConfig()
		delete(cfg, k)
		resp, body := createEmailChannel(t, srv, token, cfg)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("missing %s: status = %d, want 400", k, resp.StatusCode)
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, k+" ") {
			t.Errorf("missing %s: error %q does not name the field", k, msg)
		}
	}
	resp, body := createEmailChannel(t, srv, token, fullSMTPConfig())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("full config: status = %d, body = %v", resp.StatusCode, body)
	}
	cfg := body["config"].(map[string]interface{})
	if cfg["password"] == "org-secret" {
		t.Fatal("password returned in clear")
	}
}

func TestEmailChannel_SMTPModeRejectsPrivateHost(t *testing.T) {
	srv, _, _, token := setupEmailChannelServer(t, true)
	cfg := fullSMTPConfig()
	cfg["host"] = "127.0.0.1"
	resp, body := createEmailChannel(t, srv, token, cfg)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestEmailChannel_PlatformModeNeedsPlatformMailer(t *testing.T) {
	srv, _, _, token := setupEmailChannelServer(t, false)
	resp, body := createEmailChannel(t, srv, token, map[string]interface{}{"to": "a@org.example"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "own SMTP server") {
		t.Fatalf("error %q should point at the SMTP option", msg)
	}
	resp, body = createEmailChannel(t, srv, token, fullSMTPConfig())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("smtp mode without platform mailer: status = %d, body = %v", resp.StatusCode, body)
	}

	capResp := doJSONReq(t, http.MethodGet, srv.URL+"/api/v1/notification-channels/capabilities", token, nil)
	defer func() { _ = capResp.Body.Close() }()
	var caps map[string]interface{}
	_ = json.NewDecoder(capResp.Body).Decode(&caps)
	if capResp.StatusCode != http.StatusOK || caps["platform_email"] != false {
		t.Fatalf("capabilities = %d %v", capResp.StatusCode, caps)
	}
}

func TestEmailChannel_UpdateKeepsPasswordAndSwitchesMode(t *testing.T) {
	srv, s, _, token := setupEmailChannelServer(t, true)
	_, body := createEmailChannel(t, srv, token, fullSMTPConfig())
	id := body["id"].(string)

	// Echoing the redacted password keeps the stored one.
	cfg := fullSMTPConfig()
	cfg["password"] = "••••••••"
	cfg["to"] = "new@org.example"
	resp := doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+id, token, map[string]interface{}{"config": cfg})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d", resp.StatusCode)
	}
	if got := storedConfig(t, s, id); got["password"] != "org-secret" || got["to"] != "new@org.example" {
		t.Fatalf("stored = %v", got)
	}

	// Dropping a required field on update is rejected.
	delete(cfg, "username")
	resp = doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+id, token, map[string]interface{}{"config": cfg})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update without username: status = %d, want 400", resp.StatusCode)
	}

	// Switching to platform mode stores no server settings at all.
	resp = doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+id, token, map[string]interface{}{
		"config": map[string]interface{}{"mode": "platform", "to": "ops@org.example"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to platform: status = %d", resp.StatusCode)
	}
	got := storedConfig(t, s, id)
	if _, ok := got["password"]; ok || got["mode"] != "platform" {
		t.Fatalf("stored after switch = %v", got)
	}
}

func TestEmailChannel_TestRevalidatesLegacyRows(t *testing.T) {
	srv, s, sender, token := setupEmailChannelServer(t, true)
	user, err := s.Users().GetByEmail(context.Background(), "owner@email.test")
	if err != nil {
		t.Fatal(err)
	}
	// A row saved before the strict rules: a host with no credentials.
	legacy := &domain.NotificationChannel{
		ID: "legacy-email", OrgID: user.OrgID, Type: "smtp", Name: "Legacy", Enabled: true,
		Config: `{"host": "example.com","port":587,"from":"a@org.example","to":"ops@org.example"}`,
	}
	if err := s.NotificationChannels().Create(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/legacy-email/test", token, map[string]interface{}{})
	defer func() { _ = resp.Body.Close() }()
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body["error"], "tls_mode") {
		t.Fatalf("test: status = %d, body = %v", resp.StatusCode, body)
	}
	if sender.seen {
		t.Fatal("test sender was called for an invalid channel")
	}

	// A legacy recipients-only row tests through the platform path with the
	// canonical config.
	plat := &domain.NotificationChannel{
		ID: "legacy-platform", OrgID: user.OrgID, Type: "smtp", Name: "Legacy platform", Enabled: true,
		Config: `{"to":"ops@org.example","host":"","username":""}`,
	}
	if err := s.NotificationChannels().Create(context.Background(), plat); err != nil {
		t.Fatal(err)
	}
	resp2 := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/legacy-platform/test", token, map[string]interface{}{})
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("platform test: status = %d", resp2.StatusCode)
	}
	raw, err := base64.StdEncoding.DecodeString(sender.job.TargetConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw[1:]); got != `{"mode":"platform","to":"ops@org.example"}` {
		t.Fatalf("test job config = %s", got)
	}
}

// The channel form maps a 400 onto the input it names by reading the field
// key right after the prefix (web/src/pages/settings/email-channel-logic.mjs,
// parseEmailConfigError). Keep that shape for every email config error.
func TestEmailChannel_ErrorsNameTheFieldFirst(t *testing.T) {
	shape := regexp.MustCompile(`^invalid email channel config: (\w+) \S`)
	many := make([]string, 11)
	for i := range many {
		many[i] = fmt.Sprintf("u%d@org.example", i)
	}
	cases := []struct {
		field string
		edit  func(map[string]interface{})
	}{
		{"host", func(c map[string]interface{}) { c["host"] = "127.0.0.1" }},
		{"host", func(c map[string]interface{}) { c["host"] = "smtp://mail.org.example" }},
		{"port", func(c map[string]interface{}) { c["port"] = 70000 }},
		{"tls_mode", func(c map[string]interface{}) { c["tls_mode"] = "none" }},
		{"from", func(c map[string]interface{}) { c["from"] = "not an address" }},
		{"to", func(c map[string]interface{}) { c["to"] = "ops@org.example, nope" }},
		{"to", func(c map[string]interface{}) { c["to"] = many }},
		{"mode", func(c map[string]interface{}) { c["mode"] = "relay" }},
	}
	srv, _, _, token := setupEmailChannelServer(t, true)
	for _, tc := range cases {
		cfg := fullSMTPConfig()
		tc.edit(cfg)
		resp, body := createEmailChannel(t, srv, token, cfg)
		msg, _ := body["error"].(string)
		m := shape.FindStringSubmatch(msg)
		if resp.StatusCode != http.StatusBadRequest || m == nil || m[1] != tc.field {
			t.Errorf("want 400 naming %s first, got %d %q", tc.field, resp.StatusCode, msg)
		}
		if strings.Contains(msg, "monitor") {
			t.Errorf("email error %q talks about monitors", msg)
		}
	}

	noMailer, _, _, token2 := setupEmailChannelServer(t, false)
	_, body := createEmailChannel(t, noMailer, token2, map[string]interface{}{"to": "a@org.example"})
	if m := shape.FindStringSubmatch(body["error"].(string)); m == nil || m[1] != "mode" {
		t.Errorf("platform unavailable: error %q should name mode first", body["error"])
	}
}
