package api_test

// Endpoint smoke-test harness.
//
// Goal: every route registered across the server's route-registration files
// (the shared set plus the edition-specific variants) is exercised at least
// twice, once unauthed (proving
// the auth gate is wired) and once with a valid auth principal (proving the
// handler runs end-to-end without panicking).
//
// We are not testing semantics. A handler that responds 200, 201, 204, 400,
// 404, 409, 422, etc. is a pass; a 5xx is a CRITICAL failure (un-gated panic
// path). A 200/204 to an unauthed probe on a route that should require auth
// is a HIGH severity finding. Routes that return chi's 404 are "wired but
// unreachable" / never registered findings.
//
// Out-of-scope: WebSocket /ws (handshake is hard to assert in HTTP smoke
// shape) and the SPA static fallback /*.
//
// This file is build-tag-free and contains the shared infrastructure used
// by both edition-specific smoke suites (the SaaS build and the FOSS build).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// authMode chooses how a smoke probe authenticates.
type authMode int

const (
	// authNone makes a bare unauthenticated request. Expected to fail with
	// 401 unless the route is documented public.
	authNone authMode = iota
	// authSession uses the JWT issued at registration.
	authSession
	// authAPIKey uses an API key with the given scopes attached.
	authAPIKey
	// authAPIKeyMissingScope uses an API key whose scopes do NOT include the
	// requiredScopes, the gate should respond 403.
	authAPIKeyMissingScope
	// authOpsToken uses the ServerOptions.OpsToken Bearer.
	authOpsToken
)

// endpointCase is a single row in the smoke-test table.
type endpointCase struct {
	name         string
	method       string
	path         string
	body         any
	authMode     authMode
	wantStatuses []int
	notes        string
	// streaming marks an endpoint that holds the connection open (e.g. SSE).
	// Such a case gets a short deadline and a deadline-cut is graded as a
	// successful "stream started" (200). For every other case a deadline-cut
	// is a real failure, not a masked 200.
	streaming bool
}

// nonExistentULID is a syntactically valid but never-existing ULID/UUID-like
// value. Path-param endpoints will resolve auth/middleware first, then the
// handler will look it up and return 404, which proves the route exists
// and parses input without panicking.
const nonExistentULID = "01HXNOTFOUND0000000000000000"

// smokeFixture is the test rig: an in-memory store, a real *http.Server,
// pre-seeded session JWT, ops token, and prepared API keys.
type smokeFixture struct {
	t        *testing.T
	srv      *httptest.Server
	store    *sqlite.SQLiteStore
	hasher   *auth.APIKeyHasher
	jwt      *auth.JWTIssuer
	opsToken string

	ownerEmail   string
	sessionToken string

	// Pre-issued API keys, all owned by the same org.
	apiKeyAll      string // key with every scope yipyap recognises
	apiKeyNoScopes string // key with NO scopes (legacy "unrestricted" mode)
	apiKeyEmpty    string // key with a single irrelevant scope, used to trigger missing-scope 403
}

const smokeOpsToken = "smoke-ops-token-do-not-use-in-prod"

// newSmokeFixture spins up a fresh API server with registration enabled,
// registers an owner account, mints API keys and returns a ready-to-use rig.
func newSmokeFixture(t *testing.T) *smokeFixture {
	t.Helper()

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	hasher := auth.NewAPIKeyHasher([]byte("smoke-hasher-secret"))
	jwt := auth.NewJWTIssuer([]byte("smoke-jwt-secret-key"), time.Hour)

	handler := api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		RegistrationEnabled: true,
		APIKeyHasher:        hasher,
		OpsToken:            smokeOpsToken,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	f := &smokeFixture{
		t:        t,
		srv:      srv,
		store:    s,
		hasher:   hasher,
		jwt:      jwt,
		opsToken: smokeOpsToken,
	}

	// Register an owner; record the JWT for `authSession` probes.
	f.ownerEmail = "smoke-owner@test.com"
	f.sessionToken = f.registerOwner(f.ownerEmail, "Smoke Owner Org", "supersecret123")

	// Mint API keys.
	f.apiKeyAll = f.mintAPIKey("all-scopes", allKnownScopes())
	f.apiKeyNoScopes = f.mintAPIKey("no-scopes", nil) // empty == legacy unrestricted
	// "unrelated" scope means RequireScope(monitors) etc. all fail with 403.
	f.apiKeyEmpty = f.mintAPIKey("unrelated-scope-only", []string{"unrelated:irrelevant"})

	return f
}

// allKnownScopes returns every scope name referenced by RequireScope() in the
// router. New ones must be added here when the router grows.
func allKnownScopes() []string {
	return []string{
		"monitors",
		"alerts",
		"escalation_policies",
		"notification_channels",
		"maintenance_windows",
		// Scopes mentioned in the smoke-test brief but NOT currently used by
		// any RequireScope() in the router. We still include them so an
		// any-scope key can probe paid endpoints. Adding scopes the gate
		// never reads is harmless.
		"status_pages",
		"services",
		"incidents",
		"schedules",
		"teams",
		"events",
		"heartbeat",
		"cloudevents",
		"admin",
	}
}

func (f *smokeFixture) registerOwner(email, orgName, password string) string {
	f.t.Helper()
	body, _ := json.Marshal(map[string]string{
		"org_name": orgName,
		"email":    email,
		"password": password,
	})
	resp, err := http.Post(f.srv.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		f.t.Fatalf("register: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("register: expected 201, got %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		f.t.Fatalf("register decode: %v", err)
	}
	if out.Token == "" {
		f.t.Fatal("register: empty token (mailer-required path?)")
	}
	return out.Token
}

// mintAPIKey calls the /api/v1/org/api-keys endpoint as the owner and returns
// the plaintext key. We rely on the live HTTP path (rather than store.Create
// directly) so the key gets a real HMAC hash matched by the auth middleware.
func (f *smokeFixture) mintAPIKey(name string, scopes []string) string {
	f.t.Helper()
	body := map[string]interface{}{"name": name}
	if scopes != nil {
		body["scopes"] = scopes
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/v1/org/api-keys", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+f.sessionToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("mint api key: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		bb, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("mint api key %q: expected 201, got %d: %s", name, resp.StatusCode, string(bb))
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		f.t.Fatalf("mint api key decode: %v", err)
	}
	if !strings.HasPrefix(out.Key, "yy_") {
		f.t.Fatalf("mint api key: malformed key %q", out.Key)
	}
	return out.Key
}

// bumpToPro promotes the fixture's owner-org to the paid (Pro) plan so that
// paid-only endpoints become reachable. In FOSS this is irrelevant (the
// paid-plan gate is a no-op) but it remains harmless.
func (f *smokeFixture) bumpToPro() {
	f.t.Helper()
	ctx := context.Background()
	user, err := f.store.Users().GetByEmail(ctx, f.ownerEmail)
	if err != nil {
		f.t.Fatalf("bumpToPro lookup user: %v", err)
	}
	org, err := f.store.Orgs().GetByID(ctx, user.OrgID)
	if err != nil {
		f.t.Fatalf("bumpToPro lookup org: %v", err)
	}
	// Use a string literal plan value rather than the typed plan constant so
	// this file stays build-tag-free (that constant is defined only in the
	// SaaS build).
	org.Plan = domain.OrgPlan("pro")
	if err := f.store.Orgs().Update(ctx, org); err != nil {
		f.t.Fatalf("bumpToPro update: %v", err)
	}
}

// do issues a smoke probe. Returns the status code and a short body excerpt
// (capped) for failure messages. Uses a per-request 5s timeout so long-lived
// streams (e.g. /cloudevents/stream) cannot wedge the suite.
func (f *smokeFixture) do(c endpointCase) (int, string) {
	f.t.Helper()

	var bodyReader io.Reader
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			f.t.Fatalf("marshal body for %s %s: %v", c.method, c.path, err)
		}
		bodyReader = bytes.NewReader(b)
	}

	// Per-request deadline. Most endpoints answer in well under a second, but
	// the password-hashing routes (register/login/reset use bcrypt cost 12)
	// can take several seconds when CI runs the whole suite in parallel and
	// saturates the CPU, so give a generous ceiling. Streaming endpoints opt
	// into a short deadline instead (see endpointCase.streaming).
	timeout := 30 * time.Second
	if c.streaming {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, c.method, f.srv.URL+c.path, bodyReader)
	if err != nil {
		f.t.Fatalf("new request %s %s: %v", c.method, c.path, err)
	}
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	switch c.authMode {
	case authNone:
		// nothing.
	case authSession:
		req.Header.Set("Authorization", "Bearer "+f.sessionToken)
	case authAPIKey:
		// Use the all-scopes key.
		req.Header.Set("Authorization", "Bearer "+f.apiKeyAll)
	case authAPIKeyMissingScope:
		// Use the unrelated-scope key so RequireScope rejects.
		req.Header.Set("Authorization", "Bearer "+f.apiKeyEmpty)
	case authOpsToken:
		req.Header.Set("Authorization", "Bearer "+f.opsToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// A streaming endpoint that honoured the cancel counts as
			// "stream started" (graded against its expected statuses). For
			// any other endpoint, hitting the deadline is a real hang or a
			// too-slow handler, not a 200, so fail loudly.
			if c.streaming {
				return http.StatusOK, "(stream cut by client deadline)"
			}
			f.t.Fatalf("%s %s: no response within %s (slow handler or hang)", c.method, c.path, timeout)
		}
		f.t.Fatalf("do %s %s: %v", c.method, c.path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Use a bounded reader so we don't drain a streaming body indefinitely.
	bb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return resp.StatusCode, string(bb)
}

// runCase asserts that the response status is one of the expected ones and
//, critically, never a 5xx unless explicitly allowed.
func (f *smokeFixture) runCase(c endpointCase) {
	f.t.Helper()
	got, body := f.do(c)
	if !containsInt(c.wantStatuses, got) {
		// Distinguish 5xx (CRITICAL: un-gated panic path) from an
		// unexpected-but-non-5xx response.
		severity := "FAIL"
		if got >= 500 {
			severity = "CRITICAL-5xx"
		}
		f.t.Errorf("[%s] %s %s %s: got %d want one of %v (notes=%q) body=%s",
			severity, methodLabel(c.method), c.path, c.name, got, c.wantStatuses, c.notes, truncate(body, 256))
	}
}

func methodLabel(m string) string {
	if m == "" {
		return "GET"
	}
	return m
}

func containsInt(haystack []int, needle int) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// commonPubAuthCases returns smoke rows for the public, rate-limited /auth/*
// surface. These exist on both build flavours and document the public auth
// endpoints explicitly so future-me can confirm they were intentional.
func commonPubAuthCases() []endpointCase {
	return []endpointCase{
		// /meta is documented public, no auth, returns server feature flags.
		{name: "meta-public", method: http.MethodGet, path: "/api/v1/meta", authMode: authNone,
			wantStatuses: []int{http.StatusOK}, notes: "documented public"},

		// Public auth endpoints. Bodies kept minimal-but-valid; we expect
		// 200/201/400/401/409/422, anything but 5xx.
		{name: "auth-register", method: http.MethodPost, path: "/api/v1/auth/register", authMode: authNone,
			body:         map[string]string{"org_name": "SmokeRegOrg", "email": "smokereg+u@test.com", "password": "supersecret123"},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusConflict},
			notes:        "documented public, rate-limited"},
		{name: "auth-login", method: http.MethodPost, path: "/api/v1/auth/login", authMode: authNone,
			body:         map[string]string{"email": "noone@test.com", "password": "wrong"},
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest},
			notes:        "documented public, rate-limited"},
		{name: "auth-verify-email", method: http.MethodPost, path: "/api/v1/auth/verify-email", authMode: authNone,
			body: map[string]string{"token": "garbage"},
			// 401/400 are both valid responses to a bogus token.
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized},
			notes:        "documented public, rate-limited"},
		{name: "auth-resend-verification", method: http.MethodPost, path: "/api/v1/auth/resend-verification", authMode: authNone,
			body:         map[string]string{"email": "noone@test.com"},
			wantStatuses: []int{http.StatusOK, http.StatusAccepted, http.StatusBadRequest, http.StatusNoContent, http.StatusNotFound, http.StatusTooManyRequests},
			notes:        "documented public, rate-limited; 404 when no mailer (appears unmounted on purpose)"},
		{name: "auth-forgot-password", method: http.MethodPost, path: "/api/v1/auth/forgot-password", authMode: authNone,
			body:         map[string]string{"email": "noone@test.com"},
			wantStatuses: []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusBadRequest, http.StatusNotFound},
			notes:        "documented public, rate-limited; 404 when no mailer (appears unmounted on purpose)"},
		{name: "auth-reset-password", method: http.MethodPost, path: "/api/v1/auth/reset-password", authMode: authNone,
			body:         map[string]string{"token": "garbage", "password": "supersecret123"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized},
			notes:        "documented public, rate-limited"},
		{name: "auth-confirm-delete", method: http.MethodPost, path: "/api/v1/auth/confirm-delete", authMode: authNone,
			body:         map[string]string{"token": "garbage"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized},
			notes:        "documented public, rate-limited"},
		{name: "auth-recover-account", method: http.MethodPost, path: "/api/v1/auth/recover-account", authMode: authNone,
			body:         map[string]string{"email": "noone@test.com"},
			wantStatuses: []int{http.StatusOK, http.StatusAccepted, http.StatusBadRequest, http.StatusNoContent, http.StatusNotFound},
			notes:        "documented public, rate-limited; 404 when no mailer (appears unmounted on purpose)"},
		{name: "auth-confirm-recover", method: http.MethodPost, path: "/api/v1/auth/confirm-recover", authMode: authNone,
			body:         map[string]string{"token": "garbage"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized},
			notes:        "documented public, rate-limited"},

		// Logout requires no valid token.
		{name: "auth-logout", method: http.MethodPost, path: "/api/v1/auth/logout", authMode: authNone,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent},
			notes:        "documented public"},

		// Public maintenance lookup by org slug.
		{name: "public-maintenance", method: http.MethodGet, path: "/api/v1/public/smoke-owner-org/maintenance", authMode: authNone,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound},
			notes:        "documented public"},

		// Public status page lookup. NB: this route is mounted at the ROOT
		// (not under /api/v1) and gets caught by the SPA fallback if the
		// orgSlug is unknown, accept either status-page 404 or SPA index.
		{name: "public-status-page", method: http.MethodGet, path: "/no-such-org/status/no-such-page", authMode: authNone,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound},
			notes:        "documented public; root-mounted"},

		// Heartbeat needs a real monitor token; fake one => 401/404. Public.
		{name: "heartbeat", method: http.MethodPost, path: "/api/v1/heartbeat/" + nonExistentULID, authMode: authNone,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusUnauthorized, http.StatusBadRequest},
			notes:        "documented public; integration-key auth in body/path"},

		// Inbound events API. integration_key in body, fake => rejected.
		{name: "events-ingest", method: http.MethodPost, path: "/api/v1/events", authMode: authNone,
			body:         map[string]string{"integration_key": "no-such-key", "title": "x"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
			notes:        "documented public; integration-key auth"},

		// CloudEvents ingest by URL token. Fake token => 401.
		{name: "cloudevents-ingest", method: http.MethodPost, path: "/api/v1/cloudevents/ingest/no-such-token", authMode: authNone,
			body:         map[string]string{"specversion": "1.0", "type": "x", "id": "x", "source": "x"},
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden},
			notes:        "documented public; URL-token auth"},

		// Webhook callbacks (Discord/Telegram/Slack) verify signatures; with
		// no signing key configured, they should respond 4xx, never 5xx.
		{name: "webhook-discord", method: http.MethodPost, path: "/api/v1/webhooks/discord", authMode: authNone,
			body:         map[string]string{},
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound},
			notes:        "documented public; signature-verified; 404 when no public key configured (appears unmounted on purpose)"},
		{name: "webhook-telegram", method: http.MethodPost, path: "/api/v1/webhooks/telegram", authMode: authNone,
			body:         map[string]string{},
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden},
			notes:        "documented public; signature-verified"},
		{name: "webhook-slack", method: http.MethodPost, path: "/api/v1/integrations/slack/actions", authMode: authNone,
			body:         map[string]string{},
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden},
			notes:        "documented public; signature-verified"},
	}
}

// commonAuthedCases returns smoke rows that exist on both FOSS and full
// builds: monitors, alerts, escalations, notification channels, maintenance,
// users, org, etc.
func commonAuthedCases() []endpointCase {
	return []endpointCase{
		// Auth (authed).
		{name: "auth-refresh", method: http.MethodPost, path: "/api/v1/auth/refresh", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusUnauthorized}},
		{name: "auth-change-password", method: http.MethodPut, path: "/api/v1/auth/password", authMode: authSession,
			body:         map[string]string{"current_password": "wrong", "new_password": "anothersecret123"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized}},
		{name: "auth-change-email", method: http.MethodPut, path: "/api/v1/auth/email", authMode: authSession,
			body:         map[string]string{"email": "smoke-owner-new@test.com", "password": "supersecret123"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusConflict, http.StatusUnauthorized, http.StatusAccepted}},
		{name: "auth-delete-account", method: http.MethodPost, path: "/api/v1/auth/delete-account", authMode: authSession,
			body:         map[string]string{"password": "supersecret123"},
			wantStatuses: []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound},
			notes:        "404 when no mailer configured (appears unmounted on purpose)"},

		// Org.
		{name: "org-get", method: http.MethodGet, path: "/api/v1/org", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "org-get-settings", method: http.MethodGet, path: "/api/v1/org/settings", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "org-update", method: http.MethodPatch, path: "/api/v1/org", authMode: authSession,
			body:         map[string]string{"name": "Smoke Owner Org Renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest}},
		{name: "org-update-settings", method: http.MethodPut, path: "/api/v1/org/settings", authMode: authSession,
			body:         map[string]string{},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusNoContent, http.StatusUnprocessableEntity}},

		// Users.
		{name: "users-list", method: http.MethodGet, path: "/api/v1/users", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "users-get", method: http.MethodGet, path: "/api/v1/users/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "users-create", method: http.MethodPost, path: "/api/v1/users", authMode: authSession,
			body:         map[string]string{"email": "newuser@test.com", "role": "member"},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusConflict}},
		{name: "users-update", method: http.MethodPatch, path: "/api/v1/users/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"role": "member"},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound}},
		{name: "users-delete", method: http.MethodDelete, path: "/api/v1/users/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound, http.StatusForbidden}},
		{name: "users-reset-password", method: http.MethodPost, path: "/api/v1/users/" + nonExistentULID + "/reset-password", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusAccepted, http.StatusNotFound}},
		{name: "org-transfer-ownership", method: http.MethodPost, path: "/api/v1/org/transfer-ownership", authMode: authSession,
			body:         map[string]string{"new_owner_id": nonExistentULID},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}},

		// Monitors (RequireScope: monitors).
		{name: "monitors-list", method: http.MethodGet, path: "/api/v1/monitors", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "monitors-create", method: http.MethodPost, path: "/api/v1/monitors", authMode: authSession,
			body:         map[string]interface{}{"name": "smk-mon", "type": "http", "target": "https://example.com", "interval_seconds": 300},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusForbidden}},
		{name: "monitors-check-stats", method: http.MethodGet, path: "/api/v1/monitors/check-stats", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "monitors-get", method: http.MethodGet, path: "/api/v1/monitors/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitors-update", method: http.MethodPatch, path: "/api/v1/monitors/" + nonExistentULID, authMode: authSession,
			body:         map[string]interface{}{"name": "renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "monitors-delete", method: http.MethodDelete, path: "/api/v1/monitors/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},
		{name: "monitors-pause", method: http.MethodPost, path: "/api/v1/monitors/" + nonExistentULID + "/pause", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound}},
		{name: "monitors-resume", method: http.MethodPost, path: "/api/v1/monitors/" + nonExistentULID + "/resume", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound}},
		{name: "monitors-mute", method: http.MethodPost, path: "/api/v1/monitors/" + nonExistentULID + "/mute", authMode: authSession,
			body:         map[string]int{"duration_seconds": 60},
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest}},
		{name: "monitors-unmute", method: http.MethodPost, path: "/api/v1/monitors/" + nonExistentULID + "/unmute", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound}},
		{name: "monitors-checks", method: http.MethodGet, path: "/api/v1/monitors/" + nonExistentULID + "/checks", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitors-uptime", method: http.MethodGet, path: "/api/v1/monitors/" + nonExistentULID + "/uptime", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitors-latency", method: http.MethodGet, path: "/api/v1/monitors/" + nonExistentULID + "/latency", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitors-set-labels", method: http.MethodPut, path: "/api/v1/monitors/" + nonExistentULID + "/labels", authMode: authSession,
			body:         map[string]string{"env": "prod"},
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusBadRequest}},
		{name: "monitors-delete-label", method: http.MethodDelete, path: "/api/v1/monitors/" + nonExistentULID + "/labels/env", authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},

		// Bulk import.
		{name: "import", method: http.MethodPost, path: "/api/v1/import", authMode: authSession,
			body:         map[string]interface{}{"monitors": []interface{}{}},
			wantStatuses: []int{http.StatusOK, http.StatusBadRequest, http.StatusAccepted, http.StatusUnprocessableEntity}},

		// Admin reply audit (admin-only).
		{name: "admin-cloudevents-replies", method: http.MethodGet, path: "/api/v1/admin/cloudevents/replies", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},

		// Monitor groups.
		{name: "monitor-groups-list", method: http.MethodGet, path: "/api/v1/monitor-groups", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "monitor-groups-get", method: http.MethodGet, path: "/api/v1/monitor-groups/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitor-groups-create", method: http.MethodPost, path: "/api/v1/monitor-groups", authMode: authSession,
			body:         map[string]string{"name": "g1"},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest}},
		{name: "monitor-groups-update", method: http.MethodPatch, path: "/api/v1/monitor-groups/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"name": "renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "monitor-groups-delete", method: http.MethodDelete, path: "/api/v1/monitor-groups/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},

		// Monitor states.
		{name: "monitor-states-list", method: http.MethodGet, path: "/api/v1/monitor-states", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "monitors-rules-list", method: http.MethodGet, path: "/api/v1/monitors/" + nonExistentULID + "/rules", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "monitor-states-create", method: http.MethodPost, path: "/api/v1/monitor-states", authMode: authSession,
			body:         map[string]string{"name": "ok-ish"},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusForbidden}},
		{name: "monitor-states-update", method: http.MethodPatch, path: "/api/v1/monitor-states/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"name": "ok"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "monitor-states-delete", method: http.MethodDelete, path: "/api/v1/monitor-states/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},
		{name: "monitors-rules-replace", method: http.MethodPut, path: "/api/v1/monitors/" + nonExistentULID + "/rules", authMode: authSession,
			body:         map[string]interface{}{"rules": []interface{}{}},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest, http.StatusNoContent}},

		// Alerts.
		{name: "alerts-list", method: http.MethodGet, path: "/api/v1/alerts", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "alerts-get", method: http.MethodGet, path: "/api/v1/alerts/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "alerts-ack", method: http.MethodPost, path: "/api/v1/alerts/" + nonExistentULID + "/ack", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusConflict}},
		{name: "alerts-resolve", method: http.MethodPost, path: "/api/v1/alerts/" + nonExistentULID + "/resolve", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusConflict}},
		{name: "alerts-timeline", method: http.MethodGet, path: "/api/v1/alerts/" + nonExistentULID + "/timeline", authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},

		// Escalation policies.
		{name: "esc-policies-list", method: http.MethodGet, path: "/api/v1/escalation-policies", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "esc-policies-create", method: http.MethodPost, path: "/api/v1/escalation-policies", authMode: authSession,
			body:         map[string]string{"name": "p1"},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusForbidden}},
		{name: "esc-policies-get", method: http.MethodGet, path: "/api/v1/escalation-policies/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "esc-policies-update", method: http.MethodPatch, path: "/api/v1/escalation-policies/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"name": "renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "esc-policies-delete", method: http.MethodDelete, path: "/api/v1/escalation-policies/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},
		{name: "esc-policies-replace-steps", method: http.MethodPut, path: "/api/v1/escalation-policies/" + nonExistentULID + "/steps", authMode: authSession,
			body:         map[string]interface{}{"steps": []interface{}{}},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest, http.StatusNoContent}},

		// Notification channels.
		{name: "notif-channels-list", method: http.MethodGet, path: "/api/v1/notification-channels", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "notif-channels-create-email", method: http.MethodPost, path: "/api/v1/notification-channels", authMode: authSession,
			body:         map[string]interface{}{"name": "smk-email", "type": "email", "config": map[string]string{"to": "smk@test.com"}, "enabled": true},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest, http.StatusForbidden}},
		{name: "notif-channels-get", method: http.MethodGet, path: "/api/v1/notification-channels/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "notif-channels-update", method: http.MethodPatch, path: "/api/v1/notification-channels/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"name": "renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "notif-channels-delete", method: http.MethodDelete, path: "/api/v1/notification-channels/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},
		{name: "notif-channels-test", method: http.MethodPost, path: "/api/v1/notification-channels/" + nonExistentULID + "/test", authMode: authSession,
			body:         map[string]interface{}{},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},

		// Maintenance windows.
		{name: "maint-windows-list", method: http.MethodGet, path: "/api/v1/maintenance-windows", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "maint-windows-create", method: http.MethodPost, path: "/api/v1/maintenance-windows", authMode: authSession,
			body:         map[string]interface{}{"name": "smk", "start_at": time.Now().UTC().Format(time.RFC3339), "end_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest}},
		{name: "maint-windows-get", method: http.MethodGet, path: "/api/v1/maintenance-windows/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound}},
		{name: "maint-windows-update", method: http.MethodPatch, path: "/api/v1/maintenance-windows/" + nonExistentULID, authMode: authSession,
			body:         map[string]string{"name": "renamed"},
			wantStatuses: []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest}},
		{name: "maint-windows-delete", method: http.MethodDelete, path: "/api/v1/maintenance-windows/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},

		// API keys (FOSS+full).
		{name: "api-keys-list", method: http.MethodGet, path: "/api/v1/org/api-keys", authMode: authSession,
			wantStatuses: []int{http.StatusOK}},
		{name: "api-keys-create", method: http.MethodPost, path: "/api/v1/org/api-keys", authMode: authSession,
			body:         map[string]interface{}{"name": "smoke-extra", "scopes": []string{"monitors"}},
			wantStatuses: []int{http.StatusCreated, http.StatusBadRequest}},
		{name: "api-keys-delete", method: http.MethodDelete, path: "/api/v1/org/api-keys/" + nonExistentULID, authMode: authSession,
			wantStatuses: []int{http.StatusNoContent, http.StatusNotFound}},
	}
}

// commonUnauthCases probes every authenticated route from commonAuthedCases
// without auth and asserts 401 (or 403 in the rare case where middleware
// short-circuits to 403). 5xx is critical; 200/204 is HIGH (auth gate
// missing).
func commonUnauthCases() []endpointCase {
	authed := commonAuthedCases()
	out := make([]endpointCase, 0, len(authed))
	for _, c := range authed {
		out = append(out, endpointCase{
			name:         "unauth-" + c.name,
			method:       c.method,
			path:         c.path,
			body:         c.body,
			authMode:     authNone,
			wantStatuses: []int{http.StatusUnauthorized, http.StatusForbidden},
			notes:        "auth-gate probe",
		})
	}
	return out
}

// runAll executes every case under the fixture and is shared by the build-
// tagged front-ends.
func runAll(t *testing.T, f *smokeFixture, cases []endpointCase) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(fmt.Sprintf("%s_%s", strings.ReplaceAll(c.path, "/", "_"), c.name), func(t *testing.T) {
			f.runCase(c)
		})
	}
}
