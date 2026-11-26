package contactmethod

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
)

func TestNormalizeEmail(t *testing.T) {
	good := map[string]string{
		"a@example.com":      "a@example.com",
		"  a.b@example.co ":  "a.b@example.co",
		"first+tag@ex.co.uk": "first+tag@ex.co.uk",
	}
	for in, want := range good {
		got, err := NormalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "nope", "a@b", "Bob <b@example.com>", "a@example.com\nBcc: c@example.com",
		strings.Repeat("a", 250) + "@example.com"} {
		if _, err := NormalizeEmail(in); err == nil || !IsValidation(err) {
			t.Errorf("NormalizeEmail(%q) accepted", in)
		}
	}
}

func TestNormalizePhoneE164(t *testing.T) {
	for _, in := range []string{"", "12025550123", "+0123456789", "+1202555012a", "+1234567890123456", "+12"} {
		if _, err := NormalizePhone(in); err == nil {
			t.Errorf("NormalizePhone(%q) accepted", in)
		}
	}
	got, err := NormalizePhone(" +1 (202) 555-0123 ")
	if err != nil || got != "+12025550123" {
		t.Fatalf("NormalizePhone = %q, %v", got, err)
	}
}

func TestCodeMatches(t *testing.T) {
	code, hash, err := NewCode("m1")
	if err != nil || len(code) != 6 {
		t.Fatalf("NewCode = %q, %v", code, err)
	}
	if !CodeMatches("m1", code, hash) {
		t.Fatal("code did not match its own hash")
	}
	if CodeMatches("m2", code, hash) {
		t.Fatal("code matched for another method")
	}
	if CodeMatches("m1", code, "") {
		t.Fatal("empty stored hash matched")
	}
	other := "000000"
	if code == other {
		other = "111111"
	}
	if CodeMatches("m1", other, hash) {
		t.Fatal("wrong code matched")
	}
}

func TestTargetAndFillJob(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		m       domain.ContactMethod
		channel string
		key     string
		want    string
	}{
		{domain.ContactMethod{Type: domain.ContactEmail, Config: `{"address":"a@example.com"}`}, "smtp", "to", "a@example.com"},
		{domain.ContactMethod{Type: domain.ContactNtfy, Config: `{"topic":"t","token":"x"}`}, "ntfy", "topic", "t"},
		{domain.ContactMethod{Type: domain.ContactPushover, Config: `{"user_key":"u","api_token":"a"}`}, "pushover", "user_key", "u"},
		{domain.ContactMethod{Type: domain.ContactTelegram, Config: `{"chat_id":"42"}`}, "telegram", "chat_id", "42"},
	}
	for _, tc := range cases {
		m := tc.m
		m.ID, m.UserID, m.OrgID = "cm", "u1", "o1"
		job := domain.NotificationJob{AlertID: "a1", Message: "hi"}
		if err := FillJob(ctx, nil, &m, &job, Cipher{}); err != nil {
			t.Fatalf("%s: %v", m.Type, err)
		}
		if job.Channel != tc.channel || job.TargetUserID != "u1" || job.ContactMethodID != "cm" || job.Message != "hi" {
			t.Fatalf("%s: job = %+v", m.Type, job)
		}
		raw, _ := base64.StdEncoding.DecodeString(job.TargetConfig)
		if raw[0] != 0x00 {
			t.Fatalf("%s: want plaintext envelope", m.Type)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw[1:], &cfg); err != nil || cfg[tc.key] != tc.want {
			t.Fatalf("%s: target = %s", m.Type, raw[1:])
		}
	}

	// With an encrypter the envelope is versioned 0x01.
	m := domain.ContactMethod{ID: "cm", Type: domain.ContactEmail, Config: `{"address":"a@example.com"}`}
	job := domain.NotificationJob{}
	enc := func(p []byte) ([]byte, error) { return append([]byte("sealed:"), p...), nil }
	if err := FillJob(ctx, nil, &m, &job, Cipher{Enc: enc}); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(job.TargetConfig)
	if raw[0] != 0x01 || !strings.HasPrefix(string(raw[1:]), "sealed:") {
		t.Fatalf("encrypted envelope = %q", raw)
	}
}

func TestRedactedHidesSecrets(t *testing.T) {
	m := &domain.ContactMethod{Type: domain.ContactPushover, Config: `{"user_key":"u","api_token":"secret"}`}
	if strings.Contains(string(Redacted(m)), "secret") {
		t.Fatal("api token not redacted")
	}
	merged := mergeSecrets(domain.ContactPushover, json.RawMessage(`{"user_key":"u2","api_token":"`+Sentinel+`"}`), m.Config)
	if !strings.Contains(string(merged), `"api_token":"secret"`) || !strings.Contains(string(merged), `"user_key":"u2"`) {
		t.Fatalf("merge = %s", merged)
	}
}

func TestSummaryMasks(t *testing.T) {
	e := Summary(&domain.ContactMethod{Type: domain.ContactEmail, Config: `{"address":"alice@example.com"}`})
	if strings.Contains(e, "alice") || !strings.HasSuffix(e, "@example.com") {
		t.Fatalf("email summary = %q", e)
	}
	p := Summary(&domain.ContactMethod{Type: domain.ContactSMS, Config: `{"phone":"+12025550123"}`})
	if strings.Contains(p, "202555") || !strings.HasSuffix(p, "0123") {
		t.Fatalf("phone summary = %q", p)
	}
}

func TestNormalizePagerDutyAndWebhook(t *testing.T) {
	ctx := context.Background()

	pd, err := Normalize(ctx, nil, "o1", domain.ContactPagerDuty,
		json.RawMessage(`{"routing_key":" R0ABCDEF0123456789ABCDEF012345678 "}`), "")
	if err != nil || !strings.Contains(pd, `"R0ABCDEF0123456789ABCDEF012345678"`) {
		t.Fatalf("pagerduty normalize = %s, %v", pd, err)
	}
	if _, err := Normalize(ctx, nil, "o1", domain.ContactPagerDuty, json.RawMessage(`{"routing_key":"bad key"}`), ""); err == nil {
		t.Fatal("pagerduty accepted a short routing key")
	}

	wh, err := Normalize(ctx, nil, "o1", domain.ContactWebhook,
		json.RawMessage(`{"url":"https://example.com/hooks/yipyap","secret":"0123456789abcdef"}`), "")
	if err != nil || !strings.Contains(wh, `"url":"https://example.com/hooks/yipyap"`) {
		t.Fatalf("webhook normalize = %s, %v", wh, err)
	}
	for _, bad := range []string{
		`{"url":"not a url"}`,
		`{"url":"ftp://example.com/hook"}`,
		`{"url":"https://example.com/hook","secret":"short"}`,
	} {
		if _, err := Normalize(ctx, nil, "o1", domain.ContactWebhook, json.RawMessage(bad), ""); err == nil {
			t.Fatalf("webhook accepted %s", bad)
		}
	}
}

func TestRedactedHidesProviderTokens(t *testing.T) {
	pd := &domain.ContactMethod{Type: domain.ContactPagerDuty, Config: `{"routing_key":"R0SECRET0123456789ABCDEF012345"}`}
	if strings.Contains(string(Redacted(pd)), "R0SECRET") {
		t.Fatal("routing key not redacted")
	}
	wh := &domain.ContactMethod{Type: domain.ContactWebhook, Config: `{"url":"https://example.com/h","secret":"topsecretvalue123"}`}
	red := Redacted(wh)
	if strings.Contains(string(red), "topsecretvalue123") || !strings.Contains(string(red), Sentinel) {
		t.Fatalf("webhook config = %s", red)
	}

	merged := mergeSecrets(domain.ContactWebhook, json.RawMessage(`{"url":"https://example.com/h","secret":"`+Sentinel+`"}`), wh.Config)
	if !strings.Contains(string(merged), `"secret":"topsecretvalue123"`) {
		t.Fatalf("merge = %s", merged)
	}
}

func TestTargetPagerDutyAndWebhook(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		m       domain.ContactMethod
		channel string
		key     string
		want    any
	}{
		{domain.ContactMethod{Type: domain.ContactPagerDuty, Config: `{"routing_key":"rk"}`}, "pagerduty", "routing_key", "rk"},
		{domain.ContactMethod{Type: domain.ContactWebhook, Config: `{"url":"https://example.com/h","secret":"s"}`, ID: "cm", UserID: "u1", OrgID: "o1"}, "webhook", "format", "generic"},
	} {
		m := tc.m
		m.ID, m.UserID, m.OrgID = "cm", "u1", "o1"
		job := domain.NotificationJob{AlertID: "a1"}
		if err := FillJob(ctx, nil, &m, &job, Cipher{}); err != nil {
			t.Fatalf("%s: %v", m.Type, err)
		}
		raw, _ := base64.StdEncoding.DecodeString(job.TargetConfig)
		var cfg map[string]any
		if err := json.Unmarshal(raw[1:], &cfg); err != nil || cfg[tc.key] != tc.want {
			t.Fatalf("%s: target = %s", m.Type, raw[1:])
		}
	}
}

func TestSealAndOpenConfig(t *testing.T) {
	enc := func(p []byte) ([]byte, error) { return append([]byte("sealed:"), p...), nil }
	dec := func(p []byte) ([]byte, error) { return p[len("sealed:"):], nil }

	cfg := `{"topic":"t","token":"ntfy-token"}`
	sealed, err := SealConfig(domain.ContactNtfy, cfg, enc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "ntfy-token") || !strings.Contains(sealed, sealedPrefix) {
		t.Fatalf("sealed config = %s", sealed)
	}

	opened, err := OpenConfig(domain.ContactNtfy, sealed, dec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened, `"token":"ntfy-token"`) {
		t.Fatalf("opened config = %s", opened)
	}

	twice, err := SealConfig(domain.ContactNtfy, sealed, enc)
	if err != nil || twice != sealed {
		t.Fatalf("rescaled = %s, %v", twice, err)
	}
	if _, err := OpenConfig(domain.ContactNtfy, sealed, nil); err == nil {
		t.Fatal("opened a sealed config with no key")
	}
}

func TestSealConfigLeavesPlaintextAlone(t *testing.T) {
	cfg := `{"address":"a@example.com"}`
	got, err := SealConfig(domain.ContactEmail, cfg, func(p []byte) ([]byte, error) { return p, nil })
	if err != nil || got != cfg {
		t.Fatalf("email config = %s, %v", got, err)
	}
	got, err = OpenConfig(domain.ContactPushover, `{"user_key":"u","api_token":"plain"}`, nil)
	if err != nil || got != `{"user_key":"u","api_token":"plain"}` {
		t.Fatalf("plaintext config = %s, %v", got, err)
	}
}

// TestWebhookTargetIsSigned decodes the target config the way the webhook
// provider does and checks the owner's secret becomes the signing secret.
func TestWebhookTargetIsSigned(t *testing.T) {
	m := domain.ContactMethod{
		ID: "cm", UserID: "u1", OrgID: "o1", Type: domain.ContactWebhook,
		Config: `{"url":"https://example.com/h","secret":"0123456789abcdef"}`,
	}
	job := domain.NotificationJob{AlertID: "a1"}
	if err := FillJob(context.Background(), nil, &m, &job, Cipher{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(job.TargetConfig)
	var cfg providers.WebhookConfig
	if err := json.Unmarshal(raw[1:], &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SigningSecret != "0123456789abcdef" {
		t.Fatalf("signing secret = %q, want the contact method secret (target %s)", cfg.SigningSecret, raw[1:])
	}
	if providers.SignWebhook(cfg.SigningSecret, 1, []byte("{}")) == "" {
		t.Fatal("empty signature")
	}
}
