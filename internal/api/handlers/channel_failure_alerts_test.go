package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestChannelFailureAlertsSetting(t *testing.T) {
	env := setupPermTestEnv(t)
	const path = "/api/v1/org/channel-failure-alerts"

	resp, body := env.do(t, domain.RoleMember, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member get: status = %d", resp.StatusCode)
	}
	if body["threshold"] != float64(3) || body["min"] != float64(1) || body["max"] != float64(20) || body["can_configure"] != false {
		t.Fatalf("member get body = %v", body)
	}
	// The test server has no mailer, so the SPA must be told the emails
	// are not sent.
	if body["email_enabled"] != false {
		t.Fatalf("email_enabled = %v with no mailer, want false", body["email_enabled"])
	}

	for _, role := range []domain.UserRole{domain.RoleMember, domain.RoleViewer} {
		if resp, _ := env.do(t, role, http.MethodPut, path, map[string]int{"threshold": 5}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s put: status = %d, want 403", role, resp.StatusCode)
		}
	}

	bad := []interface{}{
		map[string]int{"threshold": 0},
		map[string]int{"threshold": 21},
		map[string]int{"threshold": -3},
		map[string]float64{"threshold": 2.5},
		map[string]string{"threshold": "5"},
		map[string]int{},
	}
	for _, b := range bad {
		if resp, out := env.do(t, domain.RoleAdmin, http.MethodPut, path, b); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %v: status = %d, want 400 (body %v)", b, resp.StatusCode, out)
		}
	}
	if v, err := env.store.OrgSettings().Get(context.Background(), orgIDOf(t, env), domain.SettingChannelFailureAlertThreshold); err == nil && v != "" {
		t.Fatalf("a rejected value was stored: %q", v)
	}

	for _, n := range []int{1, 20, 7} {
		resp, out := env.do(t, domain.RoleAdmin, http.MethodPut, path, map[string]int{"threshold": n})
		if resp.StatusCode != http.StatusOK || out["threshold"] != float64(n) {
			t.Fatalf("admin put %d: status = %d body %v", n, resp.StatusCode, out)
		}
	}
	resp, body = env.do(t, domain.RoleOwner, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK || body["threshold"] != float64(7) || body["can_configure"] != true {
		t.Fatalf("owner get after update: %d %v", resp.StatusCode, body)
	}
}

func orgIDOf(t *testing.T, env *permTestEnv) string {
	t.Helper()
	ch, err := env.store.NotificationChannels().GetByID(context.Background(), env.chanID)
	if err != nil {
		t.Fatal(err)
	}
	return ch.OrgID
}
