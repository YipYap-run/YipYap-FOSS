package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// core-monitoring decision 3: only owners and admins may change
// allow_resolve_while_down, every member can read it, and values must be
// booleans.
func TestOrgSettings_AllowResolveWhileDownRoles(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{ID: "org-resolve", Name: "R", Slug: "resolve", Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	jwt := auth.NewJWTIssuer([]byte("org-settings-test-secret"), time.Hour)
	srv := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", ""))
	t.Cleanup(srv.Close)

	tokens := map[domain.UserRole]string{}
	for _, role := range []domain.UserRole{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.RoleViewer} {
		u := &domain.User{ID: "u-" + string(role), OrgID: org.ID, Email: string(role) + "@example.com", PasswordHash: "x", Role: role, CreatedAt: now, UpdatedAt: now}
		if err := s.Users().Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, err := session.Issue(ctx, s, jwt, nil, u.ID, u.OrgID, string(u.Role), auth.AMRPassword)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = tok
	}

	do := func(role domain.UserRole, method string, body any) (int, map[string]string) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, srv.URL+"/api/v1/org/settings", rd)
		req.Header.Set("Authorization", "Bearer "+tokens[role])
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		out := map[string]string{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	on := map[string]string{"allow_resolve_while_down": "true"}
	for _, role := range []domain.UserRole{domain.RoleMember, domain.RoleViewer} {
		if code, _ := do(role, http.MethodPut, on); code != http.StatusForbidden {
			t.Fatalf("%s PUT: status %d, want 403", role, code)
		}
	}
	if v, _ := s.OrgSettings().Get(ctx, org.ID, "allow_resolve_while_down"); v == "true" {
		t.Fatal("setting changed by a member")
	}

	if code, _ := do(domain.RoleAdmin, http.MethodPut, map[string]string{"allow_resolve_while_down": "yes"}); code != http.StatusBadRequest {
		t.Fatalf("non-boolean value: status %d, want 400", code)
	}

	for _, role := range []domain.UserRole{domain.RoleOwner, domain.RoleAdmin} {
		code, got := do(role, http.MethodPut, on)
		if code != http.StatusOK || got["allow_resolve_while_down"] != "true" {
			t.Fatalf("%s PUT on: status %d body %v", role, code, got)
		}
		code, got = do(role, http.MethodPut, map[string]string{"allow_resolve_while_down": "false"})
		if code != http.StatusOK || got["allow_resolve_while_down"] != "" {
			t.Fatalf("%s PUT off: status %d body %v", role, code, got)
		}
	}

	do(domain.RoleOwner, http.MethodPut, on)
	code, got := do(domain.RoleMember, http.MethodGet, nil)
	if code != http.StatusOK || got["allow_resolve_while_down"] != "true" {
		t.Fatalf("member GET: status %d body %v", code, got)
	}
}
