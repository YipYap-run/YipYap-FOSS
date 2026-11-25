package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// Fixed vector, computed independently with Python:
//
//	hmac.new(secret, b"1700000000." + body, hashlib.sha256).hexdigest()
//
// The same vector is published in docs/webhooks.md so receivers can check
// their implementation.
const (
	vecSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vecTS     = int64(1700000000)
	vecBody   = `{"alert_id":"a1","status":"firing"}`
	vecSig    = "v1=5a83cb255e788cae0f2a49cd57a05c6af26aac65684d6fef44edc0770a4ad895"
)

func TestSignWebhook_TestVector(t *testing.T) {
	if got := SignWebhook(vecSecret, vecTS, []byte(vecBody)); got != vecSig {
		t.Fatalf("signature = %s, want %s", got, vecSig)
	}
}

func TestVerifyWebhook(t *testing.T) {
	now := time.Unix(vecTS, 0)
	ts := strconv.FormatInt(vecTS, 10)
	if err := VerifyWebhook(vecSecret, ts, vecSig, []byte(vecBody), now.Add(4*time.Minute)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// Unknown schemes are skipped, a matching v1 entry still verifies.
	if err := VerifyWebhook(vecSecret, ts, "v2=abcd, "+vecSig, []byte(vecBody), now); err != nil {
		t.Fatalf("multi-entry header rejected: %v", err)
	}
	cases := map[string]struct {
		secret, sig, body string
		now               time.Time
	}{
		"replayed":     {vecSecret, vecSig, vecBody, now.Add(6 * time.Minute)},
		"future":       {vecSecret, vecSig, vecBody, now.Add(-6 * time.Minute)},
		"wrong secret": {"whsec_other", vecSig, vecBody, now},
		"body changed": {vecSecret, vecSig, vecBody + " ", now},
		"no v1":        {vecSecret, "v0=" + strings.TrimPrefix(vecSig, "v1="), vecBody, now},
	}
	for name, c := range cases {
		if err := VerifyWebhook(c.secret, ts, c.sig, []byte(c.body), c.now); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestNewWebhookSecret(t *testing.T) {
	a, err := NewWebhookSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewWebhookSecret()
	if a == b {
		t.Fatal("two secrets are equal")
	}
	if !strings.HasPrefix(a, WebhookSecretPrefix) {
		t.Fatalf("missing prefix: %s", a)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(a, WebhookSecretPrefix))
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret should carry 32 random bytes, got %d (%v)", len(raw), err)
	}
}

func TestWebhook_SendSignsAndCarriesStatus(t *testing.T) {
	var (
		body    []byte
		headers http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(WebhookConfig{
		URL:           srv.URL,
		SigningSecret: vecSecret,
		// A custom header must not be able to replace the signature.
		Headers: map[string]string{WebhookSignatureHeader: "v1=forged"},
	})
	wh := NewWebhook(WebhookConfig{}, nil)
	wh.now = func() time.Time { return time.Unix(vecTS, 0) }
	_, err := wh.Send(context.Background(), domain.NotificationJob{
		ID: "j1", AlertID: "a1", OrgID: "o1", MonitorID: "m1", MonitorName: "api",
		Status: "resolved", Severity: "critical", Message: "back up",
		TargetConfig: string(cfg),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := headers.Get(WebhookTimestampHeader); got != strconv.FormatInt(vecTS, 10) {
		t.Fatalf("timestamp header = %q", got)
	}
	sig := headers.Get(WebhookSignatureHeader)
	if sig != SignWebhook(vecSecret, vecTS, body) {
		t.Fatalf("signature header %q does not match the body", sig)
	}
	if err := VerifyWebhook(vecSecret, headers.Get(WebhookTimestampHeader), sig, body, time.Unix(vecTS, 0)); err != nil {
		t.Fatalf("delivery does not verify: %v", err)
	}

	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p["status"] != "resolved" || p["monitor_id"] != "m1" {
		t.Fatalf("payload missing status/monitor_id: %s", body)
	}
	// Existing fields are unchanged.
	for _, k := range []string{"job_id", "alert_id", "org_id", "monitor_name", "severity", "message", "ack_url"} {
		if _, ok := p[k]; !ok {
			t.Errorf("payload lost field %s", k)
		}
	}
	if strings.Contains(string(body), vecSecret) {
		t.Fatal("secret leaked into the body")
	}
}

func TestWebhook_SendUnsignedWithoutSecret(t *testing.T) {
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh := NewWebhook(WebhookConfig{URL: srv.URL}, nil)
	if _, err := wh.Send(context.Background(), domain.NotificationJob{ID: "j"}); err != nil {
		t.Fatal(err)
	}
	if headers.Get(WebhookSignatureHeader) != "" || headers.Get(WebhookTimestampHeader) != "" {
		t.Fatal("unsigned channel sent signature headers")
	}
}
