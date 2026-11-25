package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// permTestEnv is an API server with one org and a token for each role.
type permTestEnv struct {
	srv    *httptest.Server
	store  *sqlite.SQLiteStore
	tokens map[domain.UserRole]string
	chanID string
}

func setupPermTestEnv(t *testing.T) *permTestEnv {
	t.Helper()
	e, _ := setupPermTestEnvWithSender(t)
	return e
}

// setupPermTestEnvWithSender also returns the sender Test hands jobs to.
func setupPermTestEnvWithSender(t *testing.T) (*permTestEnv, *capturingTestSender) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)
	sender := &capturingTestSender{}
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{
		TestSender:   handlers.TestSender(sender),
		APIKeyHasher: auth.NewAPIKeyHasher([]byte("perm-test-hasher-secret")),
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: uuid.NewString(), Name: "Perm Org", Slug: "perm-" + uuid.NewString()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	env := &permTestEnv{srv: srv, store: s, tokens: map[domain.UserRole]string{}}
	for _, role := range []domain.UserRole{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.RoleViewer} {
		u := &domain.User{ID: uuid.NewString(), OrgID: org.ID, Email: string(role) + "@perm.test", PasswordHash: "x", Role: role, CreatedAt: now, UpdatedAt: now}
		if err := s.Users().Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		env.tokens[role] = sessionFor(t, s, jwt, u)
	}
	env.chanID = env.createChannel(t, domain.RoleOwner, http.StatusCreated)
	return env, sender
}

func (e *permTestEnv) do(t *testing.T, role domain.UserRole, method, path string, body interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	resp := doJSONReq(t, method, e.srv.URL+path, e.tokens[role], body)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (e *permTestEnv) createChannel(t *testing.T, role domain.UserRole, want int) string {
	t.Helper()
	resp, body := e.do(t, role, http.MethodPost, "/api/v1/notification-channels", map[string]interface{}{
		"name": "Hook " + string(role), "type": "webhook", "enabled": true,
		"config": map[string]interface{}{"url": "https://example.com/x"},
	})
	if resp.StatusCode != want {
		t.Fatalf("%s create: status = %d, want %d (body %v)", role, resp.StatusCode, want, body)
	}
	id, _ := body["id"].(string)
	return id
}

// checkMatrix asserts read, test, update and create for one role.
func (e *permTestEnv) checkMatrix(t *testing.T, role domain.UserRole, canTest, canManage bool) {
	t.Helper()
	if resp, _ := e.do(t, role, http.MethodGet, "/api/v1/notification-channels", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("%s list: status = %d, want 200", role, resp.StatusCode)
	}
	wantTest := http.StatusForbidden
	if canTest {
		wantTest = http.StatusOK
	}
	if resp, body := e.do(t, role, http.MethodPost, "/api/v1/notification-channels/"+e.chanID+"/test", nil); resp.StatusCode != wantTest {
		t.Errorf("%s test: status = %d, want %d (body %v)", role, resp.StatusCode, wantTest, body)
	}
	wantPatch := http.StatusForbidden
	if canManage {
		wantPatch = http.StatusOK
	}
	if resp, body := e.do(t, role, http.MethodPatch, "/api/v1/notification-channels/"+e.chanID, map[string]interface{}{"name": "Renamed by " + string(role)}); resp.StatusCode != wantPatch {
		t.Errorf("%s update: status = %d, want %d (body %v)", role, resp.StatusCode, wantPatch, body)
	}
	wantCreate := http.StatusForbidden
	if canManage {
		wantCreate = http.StatusCreated
	}
	id := e.createChannel(t, role, wantCreate)
	if canManage {
		if resp, _ := e.do(t, role, http.MethodDelete, "/api/v1/notification-channels/"+id, nil); resp.StatusCode >= 300 {
			t.Errorf("%s delete: status = %d", role, resp.StatusCode)
		}
	} else if resp, _ := e.do(t, role, http.MethodDelete, "/api/v1/notification-channels/"+e.chanID, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("%s delete: status = %d, want 403", role, resp.StatusCode)
	}

	// The SPA reads the same answer from the permissions endpoint.
	resp, body := e.do(t, role, http.MethodGet, "/api/v1/org/notification-permissions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s get permissions: status = %d", role, resp.StatusCode)
	}
	if body["can_manage"] != canManage || body["can_test"] != canTest {
		t.Errorf("%s permissions: can_manage=%v can_test=%v, want %v %v", role, body["can_manage"], body["can_test"], canManage, canTest)
	}
	wantConfigure := role == domain.RoleOwner || role == domain.RoleAdmin
	if body["can_configure"] != wantConfigure {
		t.Errorf("%s permissions: can_configure=%v, want %v", role, body["can_configure"], wantConfigure)
	}
}

func TestChannelPermissions_DefaultMatrix(t *testing.T) {
	e := setupPermTestEnv(t)
	e.checkMatrix(t, domain.RoleViewer, false, false)
	e.checkMatrix(t, domain.RoleMember, true, false)
	e.checkMatrix(t, domain.RoleAdmin, true, true)
	e.checkMatrix(t, domain.RoleOwner, true, true)
}

func TestChannelPermissions_CustomSetting(t *testing.T) {
	e := setupPermTestEnv(t)
	resp, body := e.do(t, domain.RoleAdmin, http.MethodPut, "/api/v1/org/notification-permissions", map[string]interface{}{
		"manage_roles": []string{"admin", "member"},
		"test_roles":   []string{"viewer"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: status = %d, body %v", resp.StatusCode, body)
	}
	// Owner is always added; managers can always test.
	if got := toStrings(body["manage_roles"]); !equalStrings(got, []string{"owner", "admin", "member"}) {
		t.Errorf("manage_roles = %v", got)
	}
	if got := toStrings(body["test_roles"]); !equalStrings(got, []string{"owner", "admin", "member", "viewer"}) {
		t.Errorf("test_roles = %v", got)
	}
	e.checkMatrix(t, domain.RoleMember, true, true)
	e.checkMatrix(t, domain.RoleViewer, true, false)

	// Removing admin from managers takes effect; owner keeps access.
	if resp, body := e.do(t, domain.RoleOwner, http.MethodPut, "/api/v1/org/notification-permissions", map[string]interface{}{
		"manage_roles": []string{}, "test_roles": []string{},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("put empty: status = %d, body %v", resp.StatusCode, body)
	}
	e.checkMatrix(t, domain.RoleAdmin, false, false)
	e.checkMatrix(t, domain.RoleOwner, true, true)
}

func TestChannelPermissions_UpdateValidation(t *testing.T) {
	e := setupPermTestEnv(t)
	for _, role := range []domain.UserRole{domain.RoleMember, domain.RoleViewer} {
		resp, _ := e.do(t, role, http.MethodPut, "/api/v1/org/notification-permissions", map[string]interface{}{
			"manage_roles": []string{"admin", "member", "viewer"},
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s put: status = %d, want 403", role, resp.StatusCode)
		}
	}
	bad := []map[string]interface{}{
		{"manage_roles": []string{"viewer"}},
		{"manage_roles": []string{"superuser"}},
		{"test_roles": []string{"api_key"}},
		{},
	}
	for _, b := range bad {
		if resp, body := e.do(t, domain.RoleOwner, http.MethodPut, "/api/v1/org/notification-permissions", b); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %v: status = %d, want 400 (body %v)", b, resp.StatusCode, body)
		}
	}
	// Rejected writes leave the defaults in place.
	e.checkMatrix(t, domain.RoleMember, true, false)
}

func TestEscalationPolicyWrites_RequireAdmin(t *testing.T) {
	e := setupPermTestEnv(t)
	for _, role := range []domain.UserRole{domain.RoleMember, domain.RoleViewer} {
		if resp, _ := e.do(t, role, http.MethodGet, "/api/v1/escalation-policies", nil); resp.StatusCode != http.StatusOK {
			t.Errorf("%s list policies: status = %d, want 200", role, resp.StatusCode)
		}
		if resp, _ := e.do(t, role, http.MethodPost, "/api/v1/escalation-policies", map[string]interface{}{"name": "P"}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s create policy: status = %d, want 403", role, resp.StatusCode)
		}
	}
	resp, body := e.do(t, domain.RoleAdmin, http.MethodPost, "/api/v1/escalation-policies", map[string]interface{}{"name": "P"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create policy: status = %d (body %v)", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	for _, role := range []domain.UserRole{domain.RoleMember, domain.RoleViewer} {
		if resp, _ := e.do(t, role, http.MethodPatch, "/api/v1/escalation-policies/"+id, map[string]interface{}{"name": "X"}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s update policy: status = %d, want 403", role, resp.StatusCode)
		}
		if resp, _ := e.do(t, role, http.MethodPut, "/api/v1/escalation-policies/"+id+"/steps", []interface{}{}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s replace steps: status = %d, want 403", role, resp.StatusCode)
		}
		if resp, _ := e.do(t, role, http.MethodDelete, "/api/v1/escalation-policies/"+id, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s delete policy: status = %d, want 403", role, resp.StatusCode)
		}
	}
}

func toStrings(v interface{}) []string {
	arr, _ := v.([]interface{})
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
