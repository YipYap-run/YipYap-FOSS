package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// setupKnativeSourceConfigTest spins up an API server wired for API-key auth
// and seeds an org + an API key whose plaintext is suitable for use as a
// bearer token against /api/v1/knative/source-config.
func setupKnativeSourceConfigTest(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore, *domain.Org, string) {
	t.Helper()

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	msgBus := bus.NewChannel()
	t.Cleanup(func() { _ = msgBus.Close() })

	hasher := auth.NewAPIKeyHasher([]byte("ksc-test-hasher-secret"))
	jwt := auth.NewJWTIssuer([]byte("ksc-test-jwt-secret"), time.Hour)

	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{
		APIKeyHasher: hasher,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	org := &domain.Org{
		ID:   "org-ksc-" + ulid.Make().String(),
		Name: "KSC Test Org",
		Slug: strings.ToLower("slug-" + ulid.Make().String()),
	}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatalf("create org: %v", err)
	}

	plaintext, hash, prefix, err := hasher.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	apiKey := &domain.APIKey{
		ID:        "key-" + ulid.Make().String(),
		OrgID:     org.ID,
		Name:      "knative source",
		KeyHash:   hash,
		Prefix:    prefix,
		Scopes:    []string{"monitors:read"},
		CreatedBy: "user-ksc",
		CreatedAt: now,
	}
	if err := s.APIKeys().Create(ctx, apiKey); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	return ts, s, org, plaintext
}

// getSourceConfig issues a GET to /api/v1/knative/source-config with the
// supplied bearer token (empty = no Authorization header).
func getSourceConfig(t *testing.T, baseURL, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/knative/source-config", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

type knativeSourceConfigBody struct {
	RecommendedMode     string `json:"recommended_mode"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	RefreshAfterSeconds int    `json:"refresh_after_seconds"`
}

func decodeSourceConfig(t *testing.T, resp *http.Response) knativeSourceConfigBody {
	t.Helper()
	var body knativeSourceConfigBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("decode response: %v (raw=%q)", err, string(raw))
	}
	return body
}

// TestKnativeSourceConfig_Defaults_WhenMissing verifies that with no
// OrgSettings override in place, the endpoint returns the ship-level defaults.
func TestKnativeSourceConfig_Defaults_WhenMissing(t *testing.T) {
	ts, _, _, plaintext := setupKnativeSourceConfigTest(t)

	resp := getSourceConfig(t, ts.URL, plaintext)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	got := decodeSourceConfig(t, resp)
	want := knativeSourceConfigBody{
		RecommendedMode:     "poll",
		PollIntervalSeconds: 5,
		RefreshAfterSeconds: 300,
	}
	if got != want {
		t.Fatalf("unexpected defaults: got %+v, want %+v", got, want)
	}
}

// TestKnativeSourceConfig_OverrideAll verifies every field is picked up when
// the OrgSettings blob specifies all three.
func TestKnativeSourceConfig_OverrideAll(t *testing.T) {
	ts, s, org, plaintext := setupKnativeSourceConfigTest(t)

	override := `{"recommended_mode":"stream","poll_interval_seconds":30,"refresh_after_seconds":600}`
	if err := s.OrgSettings().Set(context.Background(), org.ID, "knative.source_config", override); err != nil {
		t.Fatalf("set org setting: %v", err)
	}

	resp := getSourceConfig(t, ts.URL, plaintext)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	got := decodeSourceConfig(t, resp)
	want := knativeSourceConfigBody{
		RecommendedMode:     "stream",
		PollIntervalSeconds: 30,
		RefreshAfterSeconds: 600,
	}
	if got != want {
		t.Fatalf("unexpected override: got %+v, want %+v", got, want)
	}
}

// TestKnativeSourceConfig_PartialOverride verifies that fields not present
// (or zero-valued) in OrgSettings fall through to the defaults.
func TestKnativeSourceConfig_PartialOverride(t *testing.T) {
	ts, s, org, plaintext := setupKnativeSourceConfigTest(t)

	// Only recommended_mode is overridden; the two interval fields omit
	// (zero-value) and must therefore retain their defaults.
	override := `{"recommended_mode":"stream"}`
	if err := s.OrgSettings().Set(context.Background(), org.ID, "knative.source_config", override); err != nil {
		t.Fatalf("set org setting: %v", err)
	}

	resp := getSourceConfig(t, ts.URL, plaintext)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	got := decodeSourceConfig(t, resp)
	want := knativeSourceConfigBody{
		RecommendedMode:     "stream",
		PollIntervalSeconds: 5,
		RefreshAfterSeconds: 300,
	}
	if got != want {
		t.Fatalf("unexpected partial override: got %+v, want %+v", got, want)
	}
}

// TestKnativeSourceConfig_InvalidModeFallsBackToPoll verifies that a stored
// recommended_mode that isn't in {"poll","stream"} is coerced to "poll".
func TestKnativeSourceConfig_InvalidModeFallsBackToPoll(t *testing.T) {
	ts, s, org, plaintext := setupKnativeSourceConfigTest(t)

	override := `{"recommended_mode":"sideways","poll_interval_seconds":7,"refresh_after_seconds":120}`
	if err := s.OrgSettings().Set(context.Background(), org.ID, "knative.source_config", override); err != nil {
		t.Fatalf("set org setting: %v", err)
	}

	resp := getSourceConfig(t, ts.URL, plaintext)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	got := decodeSourceConfig(t, resp)
	if got.RecommendedMode != "poll" {
		t.Fatalf("expected recommended_mode=poll (fallback), got %q", got.RecommendedMode)
	}
	if got.PollIntervalSeconds != 7 || got.RefreshAfterSeconds != 120 {
		t.Fatalf("other override fields should still apply: %+v", got)
	}
}

// TestKnativeSourceConfig_MalformedJSON_FallsBackToDefaults verifies that a
// corrupt OrgSettings row doesn't break the endpoint, operators shouldn't
// tip over the ContainerSource by fat-fingering JSON in a console.
func TestKnativeSourceConfig_MalformedJSON_FallsBackToDefaults(t *testing.T) {
	ts, s, org, plaintext := setupKnativeSourceConfigTest(t)

	if err := s.OrgSettings().Set(context.Background(), org.ID, "knative.source_config", "{not-json"); err != nil {
		t.Fatalf("set org setting: %v", err)
	}

	resp := getSourceConfig(t, ts.URL, plaintext)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	got := decodeSourceConfig(t, resp)
	want := knativeSourceConfigBody{
		RecommendedMode:     "poll",
		PollIntervalSeconds: 5,
		RefreshAfterSeconds: 300,
	}
	if got != want {
		t.Fatalf("malformed JSON should yield defaults: got %+v, want %+v", got, want)
	}
}

// TestKnativeSourceConfig_Unauthorized verifies that unauthenticated calls
// (no Authorization header) yield 401.
func TestKnativeSourceConfig_Unauthorized(t *testing.T) {
	ts, _, _, _ := setupKnativeSourceConfigTest(t)

	resp := getSourceConfig(t, ts.URL, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, string(raw))
	}
}
