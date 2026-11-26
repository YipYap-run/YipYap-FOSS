package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

const channelsPath = "/api/v1/notification-channels"

type configErrBody struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

// expectConfigError asserts a 400 whose body names field.
func expectConfigError(t *testing.T, resp *http.Response, field string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var body configErrBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %+v)", resp.StatusCode, body)
	}
	if body.Field != field {
		t.Fatalf("field = %q, want %q (error %q)", body.Field, field, body.Error)
	}
	if !strings.HasPrefix(body.Error, field+": ") {
		t.Fatalf("error %q does not name the field %q", body.Error, field)
	}
}

func createChannel(t *testing.T, srvURL, token, typ string, cfg any) *http.Response {
	t.Helper()
	return doJSONReq(t, http.MethodPost, srvURL+channelsPath, token, map[string]any{
		"name": typ + " channel", "type": typ, "config": cfg, "enabled": true,
	})
}

func createdID(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		var body configErrBody
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("create: status %d (%s)", resp.StatusCode, body.Error)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func proSession(t *testing.T, email string) (*httptest.Server, string, func() *configStore) {
	t.Helper()
	srv, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, srv, "Cfg "+email, email, "supersecret123")
	bumpToProByEmail(t, s, email)
	return srv, token, func() *configStore { return &configStore{s: s} }
}

// configStore reaches past the API to read or plant a stored row.
type configStore struct{ s *sqlite.SQLiteStore }

func (c *configStore) get(t *testing.T, id string) *domain.NotificationChannel {
	t.Helper()
	ch, err := c.s.NotificationChannels().GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func (c *configStore) setConfig(t *testing.T, id, cfg string) {
	t.Helper()
	ch := c.get(t, id)
	ch.Config = cfg
	if err := c.s.NotificationChannels().Update(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

// A typo in the headers textarea used to be replaced with {} and saved,
// dropping the auth header without a word.
func TestChannelConfig_WebhookHeaders(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-wh@test.com")
	cases := []struct {
		name    string
		headers any
	}{
		{"string", `{"X-Api-Key": "abc"`},
		{"number value", map[string]any{"X-Retry": 3}},
		{"header injection", map[string]any{"X-Api-Key": "abc\r\nX-Evil: 1"}},
		{"bad name", map[string]any{"X Api Key": "abc"}},
		{"managed header", map[string]any{"Host": "internal.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := createChannel(t, srv.URL, token, "webhook", map[string]any{
				"url": "https://example.com/alerts", "headers": tc.headers,
			})
			expectConfigError(t, resp, "headers")
		})
	}
	expectConfigError(t, createChannel(t, srv.URL, token, "webhook", map[string]any{
		"url": "https://example.com/alerts", "format": "msteams",
	}), "format")
}

func TestChannelConfig_RequiredAndTypes(t *testing.T) {
	srv, token, _ := proSession(t, "cfg-req@test.com")
	expectConfigError(t, createChannel(t, srv.URL, token, "slack", map[string]any{"bot_token": "xoxb-1"}), "channel_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "slack", map[string]any{"bot_token": "xoxb-1", "channel_id": "  "}), "channel_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "telegram", map[string]any{"chat_id": -100123}), "chat_id")
	expectConfigError(t, createChannel(t, srv.URL, token, "pushover", map[string]any{"api_token": "a"}), "user_key")
	expectConfigError(t, createChannel(t, srv.URL, token, "ntfy", "topic"), "config")
	expectConfigError(t, createChannel(t, srv.URL, token, "carrier_pigeon", map[string]any{}), "type")
}

// A stored row that already holds a key the schema does not know (seed data,
// older releases) can still be edited when the form echoes it back. A key
// the row did not have is still refused.
func TestChannelConfig_UpdateToleratesLegacyKeys(t *testing.T) {
	srv, token, store := proSession(t, "cfg-legacy@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "webhook", map[string]any{
		"url": "https://example.com/alerts",
	}))
	store().setConfig(t, id, `{"url":"https://example.com/alerts","method":"POST"}`)

	resp := doJSONReq(t, http.MethodPatch, srv.URL+channelsPath+"/"+id, token, map[string]any{
		"config": map[string]any{"url": "https://example.com/alerts", "method": "POST", "headers": map[string]string{"X-Api-Key": "k"}},
	})
	if resp.StatusCode != http.StatusOK {
		var body configErrBody
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("legacy echo: status %d (%s)", resp.StatusCode, body.Error)
	}
	_ = resp.Body.Close()

	resp = doJSONReq(t, http.MethodPatch, srv.URL+channelsPath+"/"+id, token, map[string]any{
		"config": map[string]any{"url": "https://example.com/alerts", "verb": "PUT"},
	})
	expectConfigError(t, resp, "verb")
}

// A row stored before the typed checks fails its test with the field to fix
// rather than sending a test the notifier cannot decode.
func TestChannelConfig_TestNamesBrokenStoredWebhookField(t *testing.T) {
	srv, token, store := proSession(t, "cfg-test-webhook@test.com")
	id := createdID(t, createChannel(t, srv.URL, token, "webhook", map[string]any{
		"url": "https://example.com/alerts",
	}))
	store().setConfig(t, id, `{"url":"https://example.com/alerts","headers":"X-Api-Key: k"}`)
	resp := doJSONReq(t, http.MethodPost, srv.URL+channelsPath+"/"+id+"/test", token, map[string]any{})
	expectConfigError(t, resp, "headers")
}
