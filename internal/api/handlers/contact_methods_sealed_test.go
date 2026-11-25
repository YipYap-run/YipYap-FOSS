package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func TestContactMethodConfigSealedAtRest(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	env, err := crypto.NewEnvelopeFromHex(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	sender := &capturingTestSender{}
	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		RegistrationEnabled: true,
		TestSender:          handlers.TestSender(sender),
		Envelope:            env,
	}))
	t.Cleanup(srv.Close)

	client := cmClient{t, srv.URL, ceRegisterAndGetToken(t, srv, "Sealed Org", "sealed@test.com", "supersecret123")}
	const token = "abcdefghij0123456789ABCDEFGHIJ"
	code, m, _ := client.do(http.MethodPost, "/users/me/contact-methods", map[string]any{
		"type": "pushover",
		"config": map[string]any{
			"user_key":  "0123456789ABCDEFGHIJKLMNOPQRST",
			"api_token": token,
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, m)
	}
	id := m["id"].(string)

	u, err := s.Users().GetByEmail(context.Background(), "sealed@test.com")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.ContactMethods().Get(context.Background(), u.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Config, token) {
		t.Fatalf("token stored in plaintext: %s", stored.Config)
	}
	if !strings.Contains(stored.Config, "sealed:v1:") {
		t.Fatalf("stored config is not sealed: %s", stored.Config)
	}

	code, got, _ := client.do(http.MethodGet, "/users/me/contact-methods/"+id, nil)
	if code != http.StatusOK {
		t.Fatalf("get: %d %v", code, got)
	}
	cfg := got["config"].(map[string]any)
	if cfg["api_token"] != "••••••••" {
		t.Fatalf("api_token = %v", cfg["api_token"])
	}
	if cfg["user_key"] != "0123456789ABCDEFGHIJKLMNOPQRST" {
		t.Fatalf("user_key = %v", cfg["user_key"])
	}

	code, updated, _ := client.do(http.MethodPatch, "/users/me/contact-methods/"+id, map[string]any{
		"config": map[string]any{
			"user_key":  "0123456789ABCDEFGHIJKLMNOPQRST",
			"api_token": "••••••••",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("patch: %d %v", code, updated)
	}
	again, err := s.ContactMethods().Get(context.Background(), u.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(again.Config, token) || !strings.Contains(again.Config, "sealed:v1:") {
		t.Fatalf("rescaled config = %s", again.Config)
	}

	if err := s.ContactMethods().MarkVerified(context.Background(), u.ID, id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	code, out, _ := client.do(http.MethodPost, "/users/me/contact-methods/"+id+"/test", nil)
	if code != http.StatusOK {
		t.Fatalf("test send: %d %v", code, out)
	}
	raw, err := base64.StdEncoding.DecodeString(sender.job.TargetConfig)
	if err != nil || len(raw) < 2 || raw[0] != 0x01 {
		t.Fatalf("target config = %q, %v", raw, err)
	}
	plain, err := env.Decrypt(raw[1:])
	if err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	if err := json.Unmarshal(plain, &target); err != nil {
		t.Fatal(err)
	}
	if target["api_token"] != token {
		t.Fatalf("delivered api_token = %v", target["api_token"])
	}
}
