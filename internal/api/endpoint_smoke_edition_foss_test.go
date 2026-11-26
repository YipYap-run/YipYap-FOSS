package api_test

// FOSS-build smoke cases. The FOSS build registers a much smaller surface:
// no MFA TOTP/WebAuthn, no SSO, no support tickets, no teams, no schedules,
// no status pages, no incidents, no OTEL integrations, no service catalog,
// no billing/Stripe, no Twilio, no JWKS, no /internal/v1/* ops surface.
//
// API keys ARE shared across editions (server_saas_extras_foss.go +
// routes_foss.go retain ListAPIKeys/CreateAPIKey/DeleteAPIKey).

import (
	"net/http"
	"testing"
)

// TestSmoke_FOSSBuild_AllEndpoints is the entry point for FOSS smoke.
func TestSmoke_FOSSBuild_AllEndpoints(t *testing.T) {
	f := newSmokeFixture(t)

	// 1. Unauthed probes for every common authed route.
	t.Run("unauth-common", func(t *testing.T) {
		runAll(t, f, commonUnauthCases())
	})

	// 2. Public auth surface.
	t.Run("public-auth", func(t *testing.T) {
		runAll(t, f, commonPubAuthCases())
	})

	// 3. Authed common routes.
	t.Run("authed-common", func(t *testing.T) {
		runAll(t, f, commonAuthedCases())
	})

	// 4. JWKS, FOSS variant is a no-op MountJWKS, so we expect 404 (or
	// SPA fallback OK from the static handler) on /.well-known/jwks.json.
	t.Run("jwks-foss-noop", func(t *testing.T) {
		f.runCase(endpointCase{
			name: "jwks-foss-not-mounted", method: http.MethodGet, path: "/.well-known/jwks.json", authMode: authNone,
			wantStatuses: []int{http.StatusOK, http.StatusNotFound},
			notes:        "FOSS no-op MountJWKS",
		})
	})

	// 5. /internal/v1/* must NOT exist on FOSS even with an ops token,
	// the handler is build-tagged out. Hitting any internal path returns
	// SPA fallback OR chi 404; never 401 (no ops middleware) and never
	// 5xx.
	t.Run("ops-foss-absent", func(t *testing.T) {
		f.runCase(endpointCase{
			name: "ops-list-orgs-foss", method: http.MethodGet, path: "/internal/v1/orgs", authMode: authOpsToken,
			wantStatuses: []int{http.StatusNotFound, http.StatusOK},
			notes:        "FOSS: /internal/v1/* is build-tagged out; SPA fallback may serve index",
		})
	})

	// 6. Twilio voice routes are absent in FOSS (registerTwilioRoutes is
	// a no-op). They should 404 (or be served by the SPA fallback).
	t.Run("twilio-foss-absent", func(t *testing.T) {
		f.runCase(endpointCase{
			name: "twilio-voice-foss", method: http.MethodPost, path: "/api/v1/twilio/voice", authMode: authNone,
			wantStatuses: []int{http.StatusNotFound, http.StatusMethodNotAllowed},
			notes:        "FOSS: registerTwilioRoutes no-op",
		})
	})
}

// TestSmoke_FOSSBuild_ScopeGate exercises scope gating in FOSS, same set
// as full because RequireScope is build-tag-blind.
func TestSmoke_FOSSBuild_ScopeGate(t *testing.T) {
	f := newSmokeFixture(t)
	cases := []endpointCase{
		{name: "scope-monitors-403", method: http.MethodGet, path: "/api/v1/monitors", authMode: authAPIKeyMissingScope,
			wantStatuses: []int{http.StatusForbidden}, notes: "RequireScope(monitors)"},
		{name: "scope-alerts-403", method: http.MethodGet, path: "/api/v1/alerts", authMode: authAPIKeyMissingScope,
			wantStatuses: []int{http.StatusForbidden}, notes: "RequireScope(alerts)"},
		{name: "scope-esc-policies-403", method: http.MethodGet, path: "/api/v1/escalation-policies", authMode: authAPIKeyMissingScope,
			wantStatuses: []int{http.StatusForbidden}, notes: "RequireScope(escalation_policies)"},
		{name: "scope-notif-channels-403", method: http.MethodGet, path: "/api/v1/notification-channels", authMode: authAPIKeyMissingScope,
			wantStatuses: []int{http.StatusForbidden}, notes: "RequireScope(notification_channels)"},
		{name: "scope-maint-windows-403", method: http.MethodGet, path: "/api/v1/maintenance-windows", authMode: authAPIKeyMissingScope,
			wantStatuses: []int{http.StatusForbidden}, notes: "RequireScope(maintenance_windows)"},
	}
	for _, c := range cases {
		f.runCase(c)
	}
}

// TestSmoke_FOSSBuild_SSRFGate confirms the SSRF gates apply on FOSS too,
// even though tier gates do not.
func TestSmoke_FOSSBuild_SSRFGate(t *testing.T) {
	f := newSmokeFixture(t)

	urls := []struct {
		field string
		url   string
		notes string
	}{
		{field: "webhook-link-local", url: "http://169.254.169.254/", notes: "AWS metadata IP"},
		{field: "webhook-private-v4", url: "http://10.0.0.1/", notes: "RFC 1918"},
		{field: "webhook-private-v6", url: "http://[fd00::1]/", notes: "ULA v6"},
		{field: "webhook-ipv4-mapped-v6", url: "http://[::169.254.169.254]/", notes: "R2-H4 regression"},
	}
	for _, u := range urls {
		c := endpointCase{
			name:   "ssrf-" + u.field,
			method: http.MethodPost,
			path:   "/api/v1/notification-channels",
			body: map[string]interface{}{
				"name": "ssrf-" + u.field, "type": "webhook",
				"config":  map[string]interface{}{"url": u.url},
				"enabled": true,
			},
			authMode:     authSession,
			wantStatuses: []int{http.StatusBadRequest},
			notes:        u.notes,
		}
		f.runCase(c)
	}
}
