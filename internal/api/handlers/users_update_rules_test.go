package handlers_test

// PATCH and DELETE /users/{id} rules the Members tab relies on (test-1).
// Role picker rules live in users_role_assign_test.go and contact-detail
// rules in users_contact_test.go; this file covers the rest: tenancy, the
// owner row, self removal, name edits and the members/viewers gate.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestUserUpdateAndRemoveRules(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "RulesOrg", "owner@rules.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@rules.test")
	if err != nil {
		t.Fatal(err)
	}
	admin := seedOrgUser(t, s, owner.OrgID, "admin@rules.test", "adminpass1", domain.RoleAdmin, "")
	member := seedOrgUser(t, s, owner.OrgID, "member@rules.test", "memberpass1", domain.RoleMember, "")
	viewer := seedOrgUser(t, s, owner.OrgID, "viewer@rules.test", "viewerpass1", domain.RoleViewer, "")
	adminToken := loginAndGetToken(t, ts, admin.Email, "adminpass1")
	memberToken := loginAndGetToken(t, ts, member.Email, "memberpass1")
	viewerToken := loginAndGetToken(t, ts, viewer.Email, "viewerpass1")

	_ = selfServisignupAndGetToken(t, ts, "OtherOrg", "owner@other.test", "otherpass1")
	outsider, err := s.Users().GetByEmail(ctx, "owner@other.test")
	if err != nil {
		t.Fatal(err)
	}

	del := func(token, id string) int {
		t.Helper()
		resp := doJSONReq(t, http.MethodDelete, ts.URL+"/api/v1/users/"+id, token, nil)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("members and viewers cannot edit or remove users", func(t *testing.T) {
		for _, tok := range []string{memberToken, viewerToken} {
			if code := patchUser(t, ts, tok, member.ID, map[string]any{"name": "x"}); code != http.StatusForbidden {
				t.Errorf("PATCH by non-admin: %d, want 403", code)
			}
			if code := patchUser(t, ts, tok, viewer.ID, map[string]any{"role": "member"}); code != http.StatusForbidden {
				t.Errorf("role change by non-admin: %d, want 403", code)
			}
			if code := del(tok, viewer.ID); code != http.StatusForbidden {
				t.Errorf("DELETE by non-admin: %d, want 403", code)
			}
		}
	})

	t.Run("users in another org are not found", func(t *testing.T) {
		if code := patchUser(t, ts, ownerToken, outsider.ID, map[string]any{"name": "hijack"}); code != http.StatusNotFound {
			t.Errorf("PATCH cross-org: %d, want 404", code)
		}
		if code := patchUser(t, ts, ownerToken, outsider.ID, map[string]any{"role": "viewer"}); code != http.StatusNotFound {
			t.Errorf("role change cross-org: %d, want 404", code)
		}
		if code := del(ownerToken, outsider.ID); code != http.StatusNotFound {
			t.Errorf("DELETE cross-org: %d, want 404", code)
		}
		got, err := s.Users().GetByID(ctx, outsider.ID)
		if err != nil || got.Role != domain.RoleOwner || got.Name == "hijack" {
			t.Fatalf("outsider changed: %+v %v", got, err)
		}
	})

	t.Run("the owner row is protected", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, owner.ID, map[string]any{"role": "viewer"}); code != http.StatusForbidden {
			t.Errorf("admin demotes owner: %d, want 403", code)
		}
		if code := del(adminToken, owner.ID); code != http.StatusForbidden {
			t.Errorf("admin removes owner: %d, want 403", code)
		}
		got, err := s.Users().GetByID(ctx, owner.ID)
		if err != nil || got.Role != domain.RoleOwner {
			t.Fatalf("owner changed: %+v %v", got, err)
		}
	})

	t.Run("nobody removes themselves", func(t *testing.T) {
		if code := del(adminToken, admin.ID); code != http.StatusBadRequest {
			t.Errorf("admin removes self: %d, want 400", code)
		}
	})

	t.Run("admins rename members and remove them", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, member.ID, map[string]any{"name": "Pat Member"}); code != http.StatusOK {
			t.Fatalf("rename: %d, want 200", code)
		}
		got, err := s.Users().GetByID(ctx, member.ID)
		if err != nil || got.Name != "Pat Member" || got.Role != domain.RoleMember {
			t.Fatalf("after rename: %+v %v", got, err)
		}
		if code := del(adminToken, member.ID); code != http.StatusNoContent && code != http.StatusOK {
			t.Fatalf("remove member: %d, want 204 or 200", code)
		}
		gone, err := s.Users().GetByID(ctx, member.ID)
		if err != nil {
			t.Fatalf("removed member row should stay as a tombstone: %v", err)
		}
		if gone.DeletedAt == nil || gone.Email == "member@example.com" {
			t.Fatalf("member was not anonymized: %+v", gone)
		}
		if code := del(adminToken, member.ID); code != http.StatusNotFound {
			t.Errorf("remove twice: %d, want 404", code)
		}
	})

	t.Run("unknown user is not found", func(t *testing.T) {
		if code := patchUser(t, ts, ownerToken, "no-such-user", map[string]any{"name": "x"}); code != http.StatusNotFound {
			t.Errorf("PATCH unknown: %d, want 404", code)
		}
	})
}

// Ownership transfer guards not covered by TestTransferOwnership: only the
// owner may start one, never to themselves, and never to a disabled admin or
// a user in another org. Each rejection leaves the owner in place.
func TestTransferOwnershipGuards(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "GuardOrg", "owner@guard.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@guard.test")
	if err != nil {
		t.Fatal(err)
	}
	admin := seedOrgUser(t, s, owner.OrgID, "admin@guard.test", "adminpass1", domain.RoleAdmin, "")
	disabled := seedOrgUser(t, s, owner.OrgID, "gone@guard.test", "gonepass1", domain.RoleAdmin, "")
	if err := s.Users().Disable(ctx, disabled.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	adminToken := loginAndGetToken(t, ts, admin.Email, "adminpass1")

	_ = selfServisignupAndGetToken(t, ts, "ElsewhereOrg", "owner@elsewhere.test", "elsewherepass1")
	outsider, err := s.Users().GetByEmail(ctx, "owner@elsewhere.test")
	if err != nil {
		t.Fatal(err)
	}

	const path = "/api/v1/org/transfer-ownership"
	steps := []struct {
		name  string
		token string
		body  map[string]any
		want  int
	}{
		{"admin cannot start a transfer", adminToken, map[string]any{"user_id": admin.ID, "current_password": "adminpass1"}, http.StatusForbidden},
		{"not to yourself", ownerToken, map[string]any{"user_id": owner.ID, "current_password": "ownerpass1"}, http.StatusBadRequest},
		{"not to another org", ownerToken, map[string]any{"user_id": outsider.ID, "current_password": "ownerpass1"}, http.StatusNotFound},
		{"not to a disabled admin", ownerToken, map[string]any{"user_id": disabled.ID, "current_password": "ownerpass1"}, http.StatusNotFound},
		{"not to an unknown user", ownerToken, map[string]any{"user_id": "no-such-user", "current_password": "ownerpass1"}, http.StatusNotFound},
	}
	for _, st := range steps {
		if code, body, _ := apiCall(t, ts, st.token, http.MethodPost, path, st.body); code != st.want {
			t.Errorf("%s: got %d %v, want %d", st.name, code, body, st.want)
		}
	}
	for id, want := range map[string]domain.UserRole{owner.ID: domain.RoleOwner, admin.ID: domain.RoleAdmin, disabled.ID: domain.RoleAdmin, outsider.ID: domain.RoleOwner} {
		got, err := s.Users().GetByID(ctx, id)
		if err != nil || got.Role != want {
			t.Errorf("user %s role = %v (%v), want %s", id, got, err, want)
		}
	}
}
