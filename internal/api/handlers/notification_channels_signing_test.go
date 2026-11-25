package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

const signingTestURL = "https://93.184.215.14/hook"

type signingChannel struct {
	ID               string          `json:"id"`
	Config           json.RawMessage `json:"config"`
	SigningSecretSet *bool           `json:"signing_secret_set"`
	SigningSecret    string          `json:"signing_secret"`
}

func setupSigningServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore, *auth.JWTIssuer, *capturingTestSender) {
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
	h := api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		RegistrationEnabled: true,
		TestSender:          handlers.TestSender(ts),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, s, jwt, ts
}

func decodeSigning(t *testing.T, resp *http.Response) signingChannel {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var ch signingChannel
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return ch
}

func createWebhook(t *testing.T, srv *httptest.Server, token string, cfg map[string]any) signingChannel {
	t.Helper()
	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels", token, map[string]any{
		"name": "hook", "type": "webhook", "enabled": true, "config": cfg,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d", resp.StatusCode)
	}
	return decodeSigning(t, resp)
}

func storedSecret(t *testing.T, s *sqlite.SQLiteStore, id string) string {
	t.Helper()
	ch, err := s.NotificationChannels().GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		SigningSecret string `json:"signing_secret"`
	}
	_ = json.Unmarshal([]byte(ch.Config), &cfg)
	return cfg.SigningSecret
}

func TestWebhookSigningSecretLifecycle(t *testing.T) {
	srv, s, _, testSender := setupSigningServer(t)
	token := ceRegisterAndGetToken(t, srv, "Sig1", "sig1@test.com", "supersecret123")

	// A client-chosen secret is ignored; the server generates one.
	created := createWebhook(t, srv, token, map[string]any{"url": signingTestURL, "signing_secret": "whsec_mine"})
	if !strings.HasPrefix(created.SigningSecret, providers.WebhookSecretPrefix) || created.SigningSecret == "whsec_mine" {
		t.Fatalf("create should return a generated secret, got %q", created.SigningSecret)
	}
	if created.SigningSecretSet == nil || !*created.SigningSecretSet {
		t.Fatal("signing_secret_set should be true")
	}
	if strings.Contains(string(created.Config), "signing_secret") {
		t.Fatalf("config in the create response carries the secret: %s", created.Config)
	}
	if got := storedSecret(t, s, created.ID); got != created.SigningSecret {
		t.Fatal("stored secret differs from the one shown")
	}

	// GET and list never return it.
	resp := doJSONReq(t, http.MethodGet, srv.URL+"/api/v1/notification-channels/"+created.ID, token, nil)
	got := decodeSigning(t, resp)
	if got.SigningSecret != "" || strings.Contains(string(got.Config), created.SigningSecret) || strings.Contains(string(got.Config), "signing_secret") {
		t.Fatalf("GET leaked the secret: %+v", got)
	}
	if got.SigningSecretSet == nil || !*got.SigningSecretSet {
		t.Fatal("GET should report signing_secret_set")
	}
	resp = doJSONReq(t, http.MethodGet, srv.URL+"/api/v1/notification-channels", token, nil)
	var list []signingChannel
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	for _, c := range list {
		if c.SigningSecret != "" || strings.Contains(string(c.Config), "signing_secret") {
			t.Fatalf("list leaked the secret: %s", c.Config)
		}
	}

	// PATCH cannot set or clear the secret, and does not echo it.
	resp = doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+created.ID, token, map[string]any{
		"config": map[string]any{"url": signingTestURL, "signing_secret": "whsec_attacker"},
	})
	patched := decodeSigning(t, resp)
	if resp.StatusCode != http.StatusOK || patched.SigningSecret != "" || strings.Contains(string(patched.Config), "signing_secret") {
		t.Fatalf("patch: status %d, body %+v", resp.StatusCode, patched)
	}
	if storedSecret(t, s, created.ID) != created.SigningSecret {
		t.Fatal("PATCH changed the signing secret")
	}
	resp = doJSONReq(t, http.MethodPatch, srv.URL+"/api/v1/notification-channels/"+created.ID, token, map[string]any{
		"config": map[string]any{"url": signingTestURL},
	})
	_ = resp.Body.Close()
	if storedSecret(t, s, created.ID) != created.SigningSecret {
		t.Fatal("PATCH without the key dropped the signing secret")
	}

	// Rotate returns a new secret once; the old one no longer verifies.
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+created.ID+"/rotate-secret", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: status %d", resp.StatusCode)
	}
	rotated := decodeSigning(t, resp)
	if rotated.SigningSecret == "" || rotated.SigningSecret == created.SigningSecret {
		t.Fatalf("rotate should return a fresh secret, got %q", rotated.SigningSecret)
	}
	newSecret := storedSecret(t, s, created.ID)
	if newSecret != rotated.SigningSecret {
		t.Fatal("rotate did not store the returned secret")
	}
	body := []byte(`{"alert_id":"a1"}`)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := providers.SignWebhook(newSecret, now.Unix(), body)
	if err := providers.VerifyWebhook(created.SigningSecret, ts, sig, body, now); err == nil {
		t.Fatal("old secret still verifies after rotate")
	}
	if err := providers.VerifyWebhook(rotated.SigningSecret, ts, sig, body, now); err != nil {
		t.Fatalf("new secret does not verify: %v", err)
	}

	// The test send carries the current secret and the new payload fields.
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+created.ID+"/test", token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test send: status %d", resp.StatusCode)
	}
	if testSender.job.Status != "firing" || testSender.job.MonitorID == "" {
		t.Fatalf("test job missing status/monitor_id: %+v", testSender.job)
	}
}

func TestWebhookRotateSecretRules(t *testing.T) {
	srv, s, jwt, _ := setupSigningServer(t)
	token := ceRegisterAndGetToken(t, srv, "Sig2", "sig2@test.com", "supersecret123")
	ch := createWebhook(t, srv, token, map[string]any{"url": signingTestURL})

	claims, err := jwt.Validate(token)
	if err != nil {
		t.Fatal(err)
	}

	// Members may test a channel but not rotate its secret.
	now := time.Now().UTC().Truncate(time.Second)
	member := &domain.User{ID: uuid.NewString(), OrgID: claims.OrgID, Email: "member@sig.test",
		PasswordHash: "x", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(context.Background(), member); err != nil {
		t.Fatal(err)
	}
	memberToken := sessionFor(t, s, jwt, member)
	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+ch.ID+"/rotate-secret", memberToken, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member rotate: status %d, want 403", resp.StatusCode)
	}
	if storedSecret(t, s, ch.ID) != ch.SigningSecret {
		t.Fatal("member rotate changed the secret")
	}

	// Another org cannot rotate it.
	other := ceRegisterAndGetToken(t, srv, "Sig3", "sig3@test.com", "supersecret123")
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+ch.ID+"/rotate-secret", other, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org rotate: status %d, want 404", resp.StatusCode)
	}

	// Non-webhook channels have no secret.
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels", token, map[string]any{
		"name": "mail", "type": "ntfy", "enabled": true, "config": map[string]any{"topic": "ops"},
	})
	email := decodeSigning(t, resp)
	if email.SigningSecret != "" || email.SigningSecretSet != nil {
		t.Fatalf("email channel got signing fields: %+v", email)
	}
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+email.ID+"/rotate-secret", token, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("email rotate: status %d, want 400", resp.StatusCode)
	}

	// A webhook created before signing existed reports unsigned and gets
	// its first secret from rotate.
	legacy := createWebhook(t, srv, token, map[string]any{"url": signingTestURL})
	stored, _ := s.NotificationChannels().GetByID(context.Background(), legacy.ID)
	stored.Config = `{"url":"` + signingTestURL + `"}`
	if err := s.NotificationChannels().Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	resp = doJSONReq(t, http.MethodGet, srv.URL+"/api/v1/notification-channels/"+legacy.ID, token, nil)
	got := decodeSigning(t, resp)
	if got.SigningSecretSet == nil || *got.SigningSecretSet {
		t.Fatalf("legacy webhook should report unsigned, got %+v", got.SigningSecretSet)
	}
	resp = doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/notification-channels/"+legacy.ID+"/rotate-secret", token, nil)
	got = decodeSigning(t, resp)
	if got.SigningSecret == "" || storedSecret(t, s, legacy.ID) != got.SigningSecret {
		t.Fatal("rotate did not give the legacy channel a secret")
	}
}

func TestBulkImportDropsWebhookSigningSecret(t *testing.T) {
	srv, s, _, _ := setupSigningServer(t)
	token := ceRegisterAndGetToken(t, srv, "Sig4", "sig4@test.com", "supersecret123")
	resp := doJSONReq(t, http.MethodPost, srv.URL+"/api/v1/import", token, map[string]any{
		"notification_channels": []map[string]any{{
			"name": "imported", "type": "webhook",
			"config": map[string]any{"url": signingTestURL, "signing_secret": "whsec_chosen"},
		}},
	})
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("import: status %d", resp.StatusCode)
	}
	resp = doJSONReq(t, http.MethodGet, srv.URL+"/api/v1/notification-channels", token, nil)
	var list []signingChannel
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(list))
	}
	if got := storedSecret(t, s, list[0].ID); got != "" {
		t.Fatalf("import kept a client-chosen secret: %q", got)
	}
}
