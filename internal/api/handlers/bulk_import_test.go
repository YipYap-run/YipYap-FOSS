package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// importResult is the import response body.
type importResult struct {
	Created         map[string]int `json:"created"`
	Errors          []string       `json:"errors"`
	Error           string         `json:"error"`
	UpgradeRequired bool           `json:"upgrade_required"`
}

func postImport(t *testing.T, url, token string, body any) (int, importResult) {
	t.Helper()
	resp := doJSONReq(t, http.MethodPost, url+"/api/v1/import", token, body)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out importResult
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func orgIDByEmail(t *testing.T, s *sqlite.SQLiteStore, email string) string {
	t.Helper()
	u, err := s.Users().GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u.OrgID
}

func countMonitors(t *testing.T, s *sqlite.SQLiteStore, orgID string) int {
	t.Helper()
	ms, err := s.Monitors().ListByOrg(context.Background(), orgID, store.MonitorFilter{ListParams: store.ListParams{Limit: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	return len(ms)
}

// TestBulkImport_ForeignReferencesRejected: a monitor may not point at
// another org's escalation policy or group, and nothing from the batch is
// created when one item fails.
func TestBulkImport_ForeignReferencesRejected(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "ImpRefs", "imprefs@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "imprefs@test.com")
	signupAndGetToken(t, ts, "OtherOrg", "other@test.com", "supersecret123")
	otherOrg := orgIDByEmail(t, s, "other@test.com")

	ctx := context.Background()
	foreignPolicy := &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: otherOrg, Name: "theirs"}
	if err := s.EscalationPolicies().Create(ctx, foreignPolicy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	foreignGroup := &domain.MonitorGroup{ID: uuid.New().String(), OrgID: otherOrg, Name: "theirs", CreatedAt: now, UpdatedAt: now}
	if err := s.MonitorGroups().Create(ctx, foreignGroup); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		field string
		value string
	}{
		{"policy", "escalation_policy_id", foreignPolicy.ID},
		{"group", "group_id", foreignGroup.ID},
		{"missing policy", "escalation_policy_id", uuid.New().String()},
	} {
		code, res := postImport(t, ts.URL, token, map[string]any{
			"teams":    []map[string]any{{"name": "Ours"}},
			"monitors": []map[string]any{{"name": "hb", "type": "heartbeat", "interval_seconds": 300, tc.field: tc.value}},
		})
		if code != http.StatusBadRequest || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], tc.field) {
			t.Errorf("%s: status = %d (%+v), want 400 naming %s", tc.name, code, res, tc.field)
		}
	}
	if n := countMonitors(t, s, orgID); n != 0 {
		t.Fatalf("created %d monitors, want 0", n)
	}
	teams, _ := s.Teams().ListByOrg(ctx, orgID, store.ListParams{})
	if len(teams) != 0 {
		t.Fatalf("created %d teams from a rejected import, want 0", len(teams))
	}
}

// TestMonitorCreate_ForeignPolicyRejected: the shared helper also closes
// the same hole on single create and update.
func TestMonitorCreate_ForeignPolicyRejected(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "CreateRefs", "createrefs@test.com", "supersecret123")
	signupAndGetToken(t, ts, "Other2", "other2@test.com", "supersecret123")
	otherOrg := orgIDByEmail(t, s, "other2@test.com")
	foreign := &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: otherOrg, Name: "theirs"}
	if err := s.EscalationPolicies().Create(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}

	resp := doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
		"name": "hb", "type": "heartbeat", "interval_seconds": 300, "escalation_policy_id": foreign.ID,
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with foreign policy: status = %d, want 400", resp.StatusCode)
	}

	resp = doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
		"name": "hb", "type": "heartbeat", "interval_seconds": 300,
	})
	var m domain.Monitor
	_ = json.NewDecoder(resp.Body).Decode(&m)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", resp.StatusCode)
	}
	resp = doJSONReq(t, http.MethodPatch, ts.URL+"/api/v1/monitors/"+m.ID, token, map[string]any{
		"name": "hb", "interval_seconds": 300, "enabled": true, "escalation_policy_id": foreign.ID,
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update to foreign policy: status = %d, want 400", resp.StatusCode)
	}

	// An unknown type is refused instead of stored.
	resp = doJSONReq(t, http.MethodPost, ts.URL+"/api/v1/monitors", token, map[string]any{
		"name": "x", "type": "smtp", "interval_seconds": 300,
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown type: status = %d, want 400", resp.StatusCode)
	}
}

// TestBulkImport_ChannelChecks: imported channels get the SSRF check that
// channel create applies. A refused channel is reported per item, so the
// rest of the import still lands.
func TestBulkImport_ChannelChecks(t *testing.T) {
	orig := checker.AllowPrivateTargets
	checker.AllowPrivateTargets = false
	defer func() { checker.AllowPrivateTargets = orig }()

	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "ImpChan", "impchan@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "impchan@test.com")

	code, res := postImport(t, ts.URL, token, map[string]any{
		"notification_channels": []map[string]any{
			{"name": "internal", "type": "webhook", "config": map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}},
		},
	})
	if code != http.StatusOK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "private or internal") {
		t.Fatalf("private webhook: status = %d (%+v), want 200 with a private-address error", code, res)
	}
	chs, _ := s.NotificationChannels().ListByOrg(context.Background(), orgID, store.ListParams{})
	if len(chs) != 0 {
		t.Fatalf("created %d channels, want 0", len(chs))
	}
}

// TestBulkImport_HeartbeatGetsToken: imported heartbeat monitors get a
// token, and the token is required to report.
func TestBulkImport_HeartbeatGetsToken(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "ImpHB", "imphb@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "imphb@test.com")

	code, res := postImport(t, ts.URL, token, map[string]any{"monitors": []map[string]any{
		{"name": "cron", "type": "heartbeat", "interval_seconds": 300, "heartbeat_token": ""},
	}})
	if code != http.StatusOK || res.Created["monitors"] != 1 {
		t.Fatalf("import: status = %d (%+v)", code, res)
	}
	ms, _ := s.Monitors().ListByOrg(context.Background(), orgID, store.MonitorFilter{})
	if len(ms) != 1 || len(ms[0].HeartbeatToken) != 32 {
		t.Fatalf("imported heartbeat monitor token = %+v, want a 32-char token", ms)
	}

	ping := func(tok string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/heartbeat/"+ms[0].ID+"?token="+tok, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if c := ping(""); c != http.StatusUnauthorized {
		t.Errorf("empty token: status = %d, want 401", c)
	}
	if c := ping(ms[0].HeartbeatToken); c != http.StatusOK {
		t.Errorf("right token: status = %d, want 200", c)
	}
}

// TestHeartbeat_EmptyStoredTokenRejected: a monitor stored without a token
// (as old imports were) must not accept an empty token. ConstantTimeCompare
// returns 1 for two empty strings.
func TestHeartbeat_EmptyStoredTokenRejected(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	signupAndGetToken(t, ts, "EmptyHB", "emptyhb@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "emptyhb@test.com")

	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.Monitor{
		ID: uuid.New().String(), OrgID: orgID, Name: "legacy", Type: domain.MonitorHeartbeat,
		IntervalSeconds: 300, TimeoutSeconds: 10, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Monitors().Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "?token="} {
		resp, err := http.Post(ts.URL+"/api/v1/heartbeat/"+m.ID+q, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("heartbeat %q on empty stored token: status = %d, want 401", q, resp.StatusCode)
		}
	}
	latest, _ := s.Checks().GetLatest(context.Background(), m.ID)
	if latest != nil {
		t.Fatalf("a check was recorded: %+v", latest)
	}
}

// TestBulkImport_WrongFormatRejected covers files in the format the old
// docs described. Before, they returned 200 with nothing created, so the
// import page reported success. Each error starts with "<section>[i]" or
// the section name, which the SPA uses to point at the item.
func TestBulkImport_WrongFormatRejected(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "ImpShape", "impshape@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "impshape@test.com")

	cases := []struct {
		name string
		body any
		want []string
	}{
		{"short section names", map[string]any{
			"channels": []any{map[string]any{"name": "c", "type": "slack"}},
			"policies": []any{map[string]any{"name": "p"}},
		}, []string{`channels: unknown section, use "notification_channels"`, `policies: unknown section, use "escalation_policies"`}},
		{"unknown section", map[string]any{"widgets": []any{}}, []string{"widgets: unknown section"}},
		{"old monitor fields", map[string]any{"monitors": []any{map[string]any{
			"name": "m", "type": "http", "url": "https://example.com", "interval": 60, "policy": "x",
		}}}, []string{`monitors[0]: unknown field "interval", use "interval_seconds"`, `monitors[0]: unknown field "policy"`, `monitors[0]: unknown field "url", use "config.url"`}},
	}
	for _, tc := range cases {
		code, res := postImport(t, ts.URL, token, tc.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%+v), want 400", tc.name, code, res)
			continue
		}
		if !strings.HasPrefix(res.Error, "Import rejected, nothing was created.") {
			t.Errorf("%s: error = %q", tc.name, res.Error)
		}
		for i, w := range tc.want {
			if i >= len(res.Errors) || !strings.HasPrefix(res.Errors[i], w) {
				t.Errorf("%s: errors = %q, want entry %d to start with %q", tc.name, res.Errors, i, w)
			}
		}
	}

	// An empty import is an error, not a successful no-op.
	for _, body := range []any{map[string]any{}, map[string]any{"monitors": []any{}}} {
		code, res := postImport(t, ts.URL, token, body)
		if code != http.StatusBadRequest || !strings.Contains(res.Error, "empty") {
			t.Errorf("empty import %v: status = %d (%+v), want 400 naming the empty import", body, code, res)
		}
	}

	// A section with the wrong type names the field.
	code, res := postImport(t, ts.URL, token, map[string]any{"monitors": map[string]any{"name": "m"}})
	if code != http.StatusBadRequest || !strings.Contains(res.Error, "monitors") {
		t.Errorf("non-list monitors: status = %d (%+v), want 400 naming monitors", code, res)
	}

	if n := countMonitors(t, s, orgID); n != 0 {
		t.Fatalf("rejected imports created %d monitors, want 0", n)
	}

	// Extra fields without a known replacement are ignored, so exported or
	// annotated files still import.
	code, res = postImport(t, ts.URL, token, map[string]any{"monitors": []any{map[string]any{
		"name": "hb", "type": "heartbeat", "interval_seconds": 300, "notes": "kept for humans",
	}}})
	if code != http.StatusOK || res.Created["monitors"] != 1 {
		t.Fatalf("import with an extra field: status = %d (%+v), want 200 with 1 created", code, res)
	}
}

// TestBulkImport_TemplateImportsOnFree imports the template the import page
// offers for download (EXAMPLE_TEMPLATE in web/src/pages/settings/import.jsx)
// into a Free org. The template used to set a 60s interval, which the Free
// plan's 300s floor rejects, so the first thing a new user tried failed.
func TestBulkImport_TemplateImportsOnFree(t *testing.T) {
	ts, s, _ := setupChannelTestServer(t)
	token := signupAndGetToken(t, ts, "ImpTpl", "imptpl@test.com", "supersecret123")
	orgID := orgIDByEmail(t, s, "imptpl@test.com")

	tpl := `{
	  "notification_channels": [{"name": "Slack Alerts", "type": "slack", "config": {"bot_token": "xoxb-...", "channel_id": "C..."}}],
	  "teams": [{"name": "Platform Team"}],
	  "escalation_policies": [{"name": "Platform On-Call"}],
	  "monitors": [
	    {"name": "API Health", "type": "http", "config": {"url": "https://example.com/", "method": "GET", "expected_status": 200}, "interval_seconds": 300, "timeout_seconds": 10},
	    {"name": "Nightly Backup", "type": "heartbeat", "interval_seconds": 86400}
	  ]
	}`
	var body map[string]any
	if err := json.Unmarshal([]byte(tpl), &body); err != nil {
		t.Fatal(err)
	}
	code, res := postImport(t, ts.URL, token, body)
	if code == http.StatusBadRequest && strings.Contains(res.Error, "does not resolve") {
		t.Skip("no DNS for example.com in this environment")
	}
	if code != http.StatusOK || len(res.Errors) != 0 {
		t.Fatalf("template import: status = %d (%+v), want 200 with no errors", code, res)
	}
	for k, want := range map[string]int{"notification_channels": 1, "teams": 1, "escalation_policies": 1, "monitors": 2} {
		if res.Created[k] != want {
			t.Errorf("created[%s] = %d, want %d", k, res.Created[k], want)
		}
	}
	if n := countMonitors(t, s, orgID); n != 2 {
		t.Fatalf("monitor count = %d, want 2", n)
	}
}
