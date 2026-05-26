package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/oidc/oidctest"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// oidcAuthConfig mirrors the on-disk JSON structure stored under
// OrgSettings key "cloudevents.ingest.auth". Defined here so tests can
// construct blobs without exporting the handler's internal type.
type oidcAuthConfig struct {
	Type string           `json:"type"`
	OIDC *oidcAuthDetails `json:"oidc,omitempty"`
}

type oidcAuthDetails struct {
	Issuer    string   `json:"issuer"`
	JWKSURL   string   `json:"jwks_url,omitempty"`
	Audiences []string `json:"audiences"`
}

func writeIngestAuth(t *testing.T, s *sqlite.SQLiteStore, orgID string, cfg oidcAuthConfig) {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal auth cfg: %v", err)
	}
	if err := s.OrgSettings().Set(context.Background(), orgID, "cloudevents.ingest.auth", string(b)); err != nil {
		t.Fatalf("set org setting: %v", err)
	}
}

// heartbeatPost sends a binary-mode heartbeat CloudEvent to the ingest
// endpoint with the given bearer token (empty = no Authorization header).
func heartbeatPost(t *testing.T, url, bearer, eventID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{"note":"oidc-test"}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", eventID)
	req.Header.Set("Ce-Source", "https://example.com/synth")
	req.Header.Set("Ce-Type", "run.yipyap.ingest.heartbeat.v1")
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

// TestIngest_OIDC_NotConfigured_PassesPhase1 confirms that when no org
// settings row exists, the Phase-1 (no auth) path still works.
func TestIngest_OIDC_NotConfigured_PassesPhase1(t *testing.T) {
	key := "ik-oidc-none"
	ts, s, _ := setupCloudEventIngestTest(t, key)

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, "", ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	_ = s
}

// TestIngest_OIDC_Required_MissingBearer_401 verifies that the Authorization
// header is required when OIDC auth is configured.
func TestIngest_OIDC_Required_MissingBearer_401(t *testing.T) {
	key := "ik-oidc-missing"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)
	idp := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idp.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, "", ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestIngest_OIDC_Required_ValidBearer_200_RecordsSub verifies that a valid
// JWT yields 200 and the verified subject is persisted on the check metadata.
func TestIngest_OIDC_Required_ValidBearer_200_RecordsSub(t *testing.T) {
	key := "ik-oidc-valid"
	ts, s, org, m := setupCloudEventIngestTestWithOrg(t, key)
	idp := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idp.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	jwt := idp.SignJWT(t, oidctest.StdClaims(idp.Issuer(), "subject-42", "alerts-ingest", time.Now()))

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwt, ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	checks := getChecks(t, s, m.ID)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(checks[0].Metadata), &meta); err != nil {
		t.Fatalf("unmarshal meta: %v (%q)", err, checks[0].Metadata)
	}
	if got := meta["oidc_sub"]; got != "subject-42" {
		t.Fatalf("oidc_sub = %v, want subject-42; full meta = %v", got, meta)
	}
}

// TestIngest_OIDC_Required_ExpiredBearer_401 verifies expired JWTs yield 401.
func TestIngest_OIDC_Required_ExpiredBearer_401(t *testing.T) {
	key := "ik-oidc-expired"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)
	idp := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idp.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	past := time.Now().Add(-2 * time.Hour)
	claims := oidctest.StdClaims(idp.Issuer(), "subject-exp", "alerts-ingest", past)
	claims["exp"] = past.Add(time.Minute).Unix() // still in the past
	jwt := idp.SignJWT(t, claims)

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwt, ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestIngest_OIDC_Required_WrongAudience_403 verifies audience mismatch → 403.
func TestIngest_OIDC_Required_WrongAudience_403(t *testing.T) {
	key := "ik-oidc-wrongaud"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)
	idp := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idp.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	jwt := idp.SignJWT(t, oidctest.StdClaims(idp.Issuer(), "sub-wrong-aud", "someone-else", time.Now()))

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwt, ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestIngest_OIDC_Required_JWKSUnavailable_503 verifies that if the JWKS
// endpoint is unreachable the response is 503 (retryable).
func TestIngest_OIDC_Required_JWKSUnavailable_503(t *testing.T) {
	key := "ik-oidc-jwks-dead"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)

	// Stand up an IdP, capture its URL, then close it so that verifier
	// init fails reproducibly.
	idp := oidctest.NewIdP(t)
	issuer := idp.Issuer()
	// Sign a JWT *before* closing so we have a legitimate-looking token.
	jwt := idp.SignJWT(t, oidctest.StdClaims(issuer, "s", "alerts-ingest", time.Now()))
	idp.Server.Close()

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    issuer,
			Audiences: []string{"alerts-ingest"},
		},
	})

	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwt, ulid.Make().String())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 503, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestIngest_OIDC_VerifierCached verifies that two consecutive successful
// requests only hit the IdP's discovery endpoint once.
func TestIngest_OIDC_VerifierCached(t *testing.T) {
	key := "ik-oidc-cached"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)
	idp := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idp.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	for i := 0; i < 2; i++ {
		jwt := idp.SignJWT(t, oidctest.StdClaims(idp.Issuer(), "sub-cache", "alerts-ingest", time.Now()))
		resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwt, ulid.Make().String())
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	if hits := idp.DiscoveryHits(); hits != 1 {
		t.Fatalf("expected discovery hit count 1 (verifier should be cached), got %d", hits)
	}
}

// TestIngest_OIDC_ConfigChange_InvalidatesVerifier verifies that changing
// the org settings invalidates the cached verifier. A JWT that was valid
// under issuer A must fail under issuer B; a JWT signed by issuer B must
// now succeed.
func TestIngest_OIDC_ConfigChange_InvalidatesVerifier(t *testing.T) {
	key := "ik-oidc-switch"
	ts, s, org, _ := setupCloudEventIngestTestWithOrg(t, key)
	idpA := oidctest.NewIdP(t)
	idpB := oidctest.NewIdP(t)

	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idpA.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	jwtA := idpA.SignJWT(t, oidctest.StdClaims(idpA.Issuer(), "sub-a", "alerts-ingest", time.Now()))
	resp := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwtA, ulid.Make().String())
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issuer A leg: expected 200, got %d", resp.StatusCode)
	}

	// Swap issuer.
	writeIngestAuth(t, s, org.ID, oidcAuthConfig{
		Type: "oidc",
		OIDC: &oidcAuthDetails{
			Issuer:    idpB.Issuer(),
			Audiences: []string{"alerts-ingest"},
		},
	})

	// Old A-issued token should no longer verify, its iss does not match
	// idpB's discovery, so the verifier rejects it. Expect 401 (wrong
	// issuer maps to 403, invalid signature/etc maps to 401, either is a
	// rejection). We assert non-200.
	resp2 := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwtA, ulid.Make().String())
	_ = resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("issuer A token should not succeed under issuer B config, got 200")
	}

	// New B-issued token should succeed.
	jwtB := idpB.SignJWT(t, oidctest.StdClaims(idpB.Issuer(), "sub-b", "alerts-ingest", time.Now()))
	resp3 := heartbeatPost(t, ts.URL+"/api/v1/cloudevents/ingest/"+key, jwtB, ulid.Make().String())
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp3.Body)
		t.Fatalf("issuer B leg: expected 200, got %d: %s", resp3.StatusCode, string(b))
	}
}

