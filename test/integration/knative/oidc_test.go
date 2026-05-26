//go:build knative

// Package knative (OIDC ingest side): exercises the inbound OIDC-gated
// CloudEvents ingest path end-to-end against a local httptest IdP (no real
// Knative cluster). These tests prove wire-compatibility with the JWT-bearer
// auth model that Knative Eventing's EventPolicy produces; a real-cluster
// round-trip is deferred until the kind harness is stable on CI.
package knative

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/oidc/oidctest"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// testAudience is a stable, non-guessable audience string the OIDC tests
// seed into both the OrgSettings config and the signed JWTs.
const testAudience = "https://test/ingest"

// oidcIngestAuthConfig mirrors the OrgSettings JSON shape the handler reads
// to gate ingest on an OIDC bearer. Kept local so we don't depend on the
// handler package's unexported struct.
type oidcIngestAuthConfig struct {
	Type string                `json:"type"`
	OIDC *oidcIngestAuthOIDC   `json:"oidc,omitempty"`
}

type oidcIngestAuthOIDC struct {
	Issuer    string   `json:"issuer"`
	JWKSURL   string   `json:"jwks_url,omitempty"`
	Audiences []string `json:"audiences"`
}

// setupOIDCIngestEnv spins up a sqlite store + org + heartbeat monitor, mounts
// the CloudEventHandler Ingest route on an httptest server, and returns the
// handles a test needs to drive end-to-end OIDC ingest.
//
// Helpers are intentionally per-file so each integration scenario is
// self-contained, the package-level `newTempStore` / `seedHeartbeatMonitor`
// in inbound_test.go return a narrow store.Store and don't expose the
// concrete sqlite.SQLiteStore we need for OrgSettings writes.
func setupOIDCIngestEnv(t *testing.T) (ts *httptest.Server, s *sqlite.SQLiteStore, org *domain.Org, monitor *domain.Monitor) {
	t.Helper()

	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	o := &domain.Org{
		ID:        "org-oidc-" + ulid.Make().String(),
		Name:      "knative-oidc-test",
		Slug:      "oidc-" + ulid.Make().String(),
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Orgs().Create(ctx, o); err != nil {
		t.Fatalf("Orgs.Create: %v", err)
	}

	m := &domain.Monitor{
		ID:              "mon-oidc-" + ulid.Make().String(),
		OrgID:           o.ID,
		Name:            "oidc-heartbeat",
		Type:            domain.MonitorHeartbeat,
		Config:          json.RawMessage(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		IntegrationKey:  "knative-oidc-token-" + ulid.Make().String(),
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.Monitors().Create(ctx, m); err != nil {
		t.Fatalf("Monitors.Create: %v", err)
	}

	h := handlers.NewCloudEventHandler(store)
	r := chi.NewRouter()
	r.Post("/api/v1/cloudevents/ingest/{token}", h.Ingest)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv, store, o, m
}

// writeIngestOIDCAuth persists the per-org OIDC ingest auth config under the
// canonical OrgSettings key.
func writeIngestOIDCAuth(t *testing.T, s *sqlite.SQLiteStore, orgID, issuer string, audiences []string) {
	t.Helper()
	cfg := oidcIngestAuthConfig{
		Type: "oidc",
		OIDC: &oidcIngestAuthOIDC{
			Issuer:    issuer,
			Audiences: audiences,
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal auth cfg: %v", err)
	}
	if err := s.OrgSettings().Set(context.Background(), orgID, "cloudevents.ingest.auth", string(b)); err != nil {
		t.Fatalf("OrgSettings.Set: %v", err)
	}
}

// postHeartbeatWithBearer posts a binary-mode heartbeat CloudEvent against
// the ingest URL, optionally attaching an Authorization: Bearer header.
func postHeartbeatWithBearer(t *testing.T, ingestURL, bearer string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"note": "knative-oidc-integration"})
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ingestURL,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", ulid.Make().String())
	req.Header.Set("Ce-Source", "https://broker-ingress.knative-eventing.svc.cluster.local/test/default")
	req.Header.Set("Ce-Type", types.TypeIngestHeartbeatV1)
	req.Header.Set("Ce-Time", time.Now().UTC().Format(time.RFC3339Nano))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// drainBody reads + closes resp.Body, returning the body as a string (best
// effort; ignored on error). Used for richer failure messages.
func drainBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(b)
}

// TestKnative_Ingest_OIDC_Required_PassesWithValidBearer, OrgSettings
// configures OIDC auth with issuer+audience; a JWT whose `iss`, `aud`, and
// `sub` match and whose `exp` is in the future yields 200 OK and the
// verified subject is persisted onto the written check's metadata.
func TestKnative_Ingest_OIDC_Required_PassesWithValidBearer(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	ts, s, org, m := setupOIDCIngestEnv(t)
	idp := oidctest.NewIdP(t)

	writeIngestOIDCAuth(t, s, org.ID, idp.Issuer(), []string{testAudience})

	claims := oidctest.StdClaims(idp.Issuer(), "system:serviceaccount:ns:my-sa", testAudience, time.Now())
	jwt := idp.SignJWT(t, claims)

	resp := postHeartbeatWithBearer(t, ts.URL+"/api/v1/cloudevents/ingest/"+m.IntegrationKey, jwt)
	body := drainBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	checks, err := s.Checks().ListByMonitor(context.Background(), m.ID,
		store.CheckFilter{ListParams: store.ListParams{Limit: 10}})
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("no check recorded after successful OIDC-authed ingest")
	}
	if checks[0].Status != domain.StatusUp {
		t.Errorf("check status = %s, want up", checks[0].Status)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(checks[0].Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata %q: %v", checks[0].Metadata, err)
	}
	if got := meta["oidc_sub"]; got != "system:serviceaccount:ns:my-sa" {
		t.Errorf("oidc_sub = %v, want %q; meta=%v", got, "system:serviceaccount:ns:my-sa", meta)
	}
}

// TestKnative_Ingest_OIDC_Required_401MissingBearer, OIDC configured but
// no Authorization header on the request → 401.
func TestKnative_Ingest_OIDC_Required_401MissingBearer(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	ts, s, org, m := setupOIDCIngestEnv(t)
	idp := oidctest.NewIdP(t)

	writeIngestOIDCAuth(t, s, org.ID, idp.Issuer(), []string{testAudience})

	resp := postHeartbeatWithBearer(t, ts.URL+"/api/v1/cloudevents/ingest/"+m.IntegrationKey, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, drainBody(resp))
	}
}

// TestKnative_Ingest_OIDC_Required_403WrongAudience, a JWT whose `aud` does
// not include the audience the OrgSettings config expects is rejected with
// 403 (authenticated identity, but forbidden access).
func TestKnative_Ingest_OIDC_Required_403WrongAudience(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	ts, s, org, m := setupOIDCIngestEnv(t)
	idp := oidctest.NewIdP(t)

	writeIngestOIDCAuth(t, s, org.ID, idp.Issuer(), []string{testAudience})

	wrong := "https://some-other-aud/invalid"
	jwt := idp.SignJWT(t, oidctest.StdClaims(idp.Issuer(), "someone", wrong, time.Now()))

	resp := postHeartbeatWithBearer(t, ts.URL+"/api/v1/cloudevents/ingest/"+m.IntegrationKey, jwt)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", resp.StatusCode, drainBody(resp))
	}
}

// TestKnative_Ingest_OIDC_Required_401Expired, a JWT whose `exp` is in the
// past is rejected with 401 (not 403: the caller's claim of identity is
// stale, so re-auth is the remedy).
func TestKnative_Ingest_OIDC_Required_401Expired(t *testing.T) {
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
	ts, s, org, m := setupOIDCIngestEnv(t)
	idp := oidctest.NewIdP(t)

	writeIngestOIDCAuth(t, s, org.ID, idp.Issuer(), []string{testAudience})

	past := time.Now().Add(-2 * time.Hour)
	claims := oidctest.StdClaims(idp.Issuer(), "expired-sub", testAudience, past)
	// StdClaims sets exp = past+5m, which is still in the past; pin it
	// explicitly to make the intent obvious in failure diagnostics.
	claims["exp"] = past.Add(1 * time.Minute).Unix()
	jwt := idp.SignJWT(t, claims)

	resp := postHeartbeatWithBearer(t, ts.URL+"/api/v1/cloudevents/ingest/"+m.IntegrationKey, jwt)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, drainBody(resp))
	}
}
