package handlers_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

const (
	redactSlackURL  = "https://hooks.slack.com/services/T0001/B0002/zq8RkTokenValue42"
	redactAuthValue = "Bearer hdr-s3cret-value"
	sentinel        = "••••••••"
)

// mintKey creates an API key with the notification_channels scopes as the
// owner.
func (e *permTestEnv) mintKey(t *testing.T) string {
	t.Helper()
	resp, body := e.do(t, domain.RoleOwner, http.MethodPost, "/api/v1/org/api-keys", map[string]interface{}{
		"name": "channels", "scopes": []string{"notification_channels:read", "notification_channels:write"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint api key: status %d (%v)", resp.StatusCode, body)
	}
	key, _ := body["key"].(string)
	return key
}

func rawGet(t *testing.T, url, token string) []byte {
	t.Helper()
	resp := doJSONReq(t, http.MethodGet, url, token, nil)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d (%s)", url, resp.StatusCode, b)
	}
	return b
}

// assertNoSecrets fails when body holds a header value or the full URL.
func assertNoSecrets(t *testing.T, who string, body []byte) {
	t.Helper()
	for _, secret := range []string{"hdr-s3cret-value", "zq8RkTokenValue42", "/services/", "ops-team-value"} {
		if bytes.Contains(body, []byte(secret)) {
			t.Errorf("%s: response leaks %q: %s", who, secret, body)
		}
	}
}

func createSecretWebhook(t *testing.T, e *permTestEnv) (string, map[string]interface{}) {
	t.Helper()
	resp, body := e.do(t, domain.RoleOwner, http.MethodPost, "/api/v1/notification-channels", map[string]interface{}{
		"name": "Slack hook", "type": "webhook", "enabled": true,
		"config": map[string]interface{}{
			"url": redactSlackURL, "format": "slack",
			"headers": map[string]interface{}{
				"Authorization": redactAuthValue,
				"X-Team":        "ops-team-value",
				"X-Remove":      "gone-soon",
			},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", resp.StatusCode, body)
	}
	return body["id"].(string), body
}

func TestChannelRedaction_ReadsNeverReturnHeaderValuesOrIncomingURL(t *testing.T) {
	e := setupPermTestEnv(t)
	id, created := createSecretWebhook(t, e)
	b, _ := json.Marshal(created)
	assertNoSecrets(t, "create response", b)

	tokens := map[string]string{"api_key": e.mintKey(t)}
	for role, tok := range e.tokens {
		tokens[string(role)] = tok
	}
	for who, tok := range tokens {
		assertNoSecrets(t, who+" list", rawGet(t, e.srv.URL+"/api/v1/notification-channels", tok))
		one := rawGet(t, e.srv.URL+"/api/v1/notification-channels/"+id, tok)
		assertNoSecrets(t, who+" get", one)

		var ch struct {
			Config struct {
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"config"`
			URLField string `json:"url_field"`
			URLHost  string `json:"url_host"`
			URLSet   bool   `json:"url_set"`
		}
		if err := json.Unmarshal(one, &ch); err != nil {
			t.Fatal(err)
		}
		if ch.Config.URL != sentinel || !ch.URLSet || ch.URLHost != "https://hooks.slack.com" || ch.URLField != "url" {
			t.Errorf("%s: url=%q url_set=%v url_host=%q url_field=%q", who, ch.Config.URL, ch.URLSet, ch.URLHost, ch.URLField)
		}
		if len(ch.Config.Headers) != 3 || ch.Config.Headers["Authorization"] != sentinel {
			t.Errorf("%s: headers = %v, want names with sentinel values", who, ch.Config.Headers)
		}
	}
}

func TestChannelRedaction_PatchKeepsReplacesAndDeletes(t *testing.T) {
	e := setupPermTestEnv(t)
	id, _ := createSecretWebhook(t, e)
	path := "/api/v1/notification-channels/" + id

	// Echo the redacted form: sentinel URL and Authorization kept, X-Team
	// replaced, X-Remove left out (deleted), X-New added.
	resp, body := e.do(t, domain.RoleOwner, http.MethodPatch, path, map[string]interface{}{
		"config": map[string]interface{}{
			"url": sentinel, "format": "slack",
			"headers": map[string]interface{}{
				"Authorization": sentinel,
				"X-Team":        "dev",
				"X-New":         "fresh",
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: status %d (%v)", resp.StatusCode, body)
	}
	b, _ := json.Marshal(body)
	assertNoSecrets(t, "patch response", b)

	stored := e.storedConfig(t, id)
	if stored["url"] != redactSlackURL {
		t.Errorf("url = %v, want stored URL kept", stored["url"])
	}
	h, _ := stored["headers"].(map[string]interface{})
	if h["Authorization"] != redactAuthValue || h["X-Team"] != "dev" || h["X-New"] != "fresh" {
		t.Errorf("headers = %v", h)
	}
	if _, ok := h["X-Remove"]; ok {
		t.Errorf("X-Remove should be deleted: %v", h)
	}

	// A sentinel with no stored value behind it is refused, not saved.
	resp = doJSONReq(t, http.MethodPatch, e.srv.URL+path, e.tokens[domain.RoleOwner], map[string]interface{}{
		"config": map[string]interface{}{
			"url": sentinel, "headers": map[string]interface{}{"X-Unknown": sentinel},
		},
	})
	expectConfigError(t, resp, "headers.X-Unknown")

	// A new URL replaces the stored one.
	const newURL = "https://hooks.slack.com/services/T9/B9/rotatedToken123456"
	resp, body = e.do(t, domain.RoleOwner, http.MethodPatch, path, map[string]interface{}{
		"config": map[string]interface{}{"url": newURL, "format": "slack"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch url: status %d (%v)", resp.StatusCode, body)
	}
	if got := e.storedConfig(t, id); got["url"] != newURL {
		t.Errorf("url = %v, want %s", got["url"], newURL)
	}
	if _, ok := e.storedConfig(t, id)["headers"]; ok {
		t.Errorf("headers left out of the config should be gone")
	}
}

func TestChannelRedaction_CreateRefusesSentinel(t *testing.T) {
	e := setupPermTestEnv(t)
	resp := doJSONReq(t, http.MethodPost, e.srv.URL+"/api/v1/notification-channels", e.tokens[domain.RoleOwner], map[string]interface{}{
		"name": "x", "type": "webhook",
		"config": map[string]interface{}{"url": "https://example.com/a", "headers": map[string]interface{}{"X-Key": sentinel}},
	})
	expectConfigError(t, resp, "headers.X-Key")
	resp = doJSONReq(t, http.MethodPost, e.srv.URL+"/api/v1/notification-channels", e.tokens[domain.RoleOwner], map[string]interface{}{
		"name": "x", "type": "webhook", "config": map[string]interface{}{"url": sentinel},
	})
	expectConfigError(t, resp, "url")
}

// A plain endpoint URL with no credential in it stays readable.
func TestChannelRedaction_PlainURLStaysVisible(t *testing.T) {
	e := setupPermTestEnv(t)
	one := rawGet(t, e.srv.URL+"/api/v1/notification-channels/"+e.chanID, e.tokens[domain.RoleViewer])
	var ch map[string]interface{}
	_ = json.Unmarshal(one, &ch)
	cfg := ch["config"].(map[string]interface{})
	if cfg["url"] != "https://example.com/x" {
		t.Errorf("url = %v, want it shown", cfg["url"])
	}
	if _, ok := ch["url_set"]; ok {
		t.Errorf("url_set should be absent for a visible URL: %s", one)
	}
}

// Delivery (here the Test endpoint) sends with the stored secrets, not
// the sentinel.
func TestChannelRedaction_DeliveryUsesStoredSecrets(t *testing.T) {
	e, sender := setupPermTestEnvWithSender(t)
	id, _ := createSecretWebhook(t, e)
	// Round-trip the redacted config through PATCH first, as the form does.
	resp, body := e.do(t, domain.RoleOwner, http.MethodPatch, "/api/v1/notification-channels/"+id, map[string]interface{}{
		"config": map[string]interface{}{
			"url": sentinel, "format": "slack",
			"headers": map[string]interface{}{"Authorization": sentinel},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: status %d (%v)", resp.StatusCode, body)
	}
	resp, body = e.do(t, domain.RoleOwner, http.MethodPost, "/api/v1/notification-channels/"+id+"/test", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test: status %d (%v)", resp.StatusCode, body)
	}
	sender.mu.Lock()
	job := sender.job
	sender.mu.Unlock()
	raw, err := base64.StdEncoding.DecodeString(job.TargetConfig)
	if err != nil || len(raw) == 0 {
		t.Fatalf("decode target config: %v", err)
	}
	sent := string(raw[1:])
	if !strings.Contains(sent, redactSlackURL) || !strings.Contains(sent, redactAuthValue) || strings.Contains(sent, sentinel) {
		t.Errorf("sent config = %s, want stored URL and header value", sent)
	}
}

func (e *permTestEnv) storedConfig(t *testing.T, id string) map[string]interface{} {
	t.Helper()
	return storedConfig(t, e.store, id)
}
