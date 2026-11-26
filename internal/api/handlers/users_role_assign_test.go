package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// TestRoleAssignmentContract pins the rules the SPA role picker mirrors in
// assignableRoles (web/src/state/permissions.mjs): the owner may grant admin,
// member or viewer; an admin may grant member or viewer but not admin; nobody
// sets owner through a role change.
func TestRoleAssignmentContract(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "RoleOrg", "owner@roles.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@roles.test")
	if err != nil {
		t.Fatal(err)
	}
	admin := seedOrgUser(t, s, owner.OrgID, "admin@roles.test", "adminpass1", domain.RoleAdmin, "")
	target := seedOrgUser(t, s, owner.OrgID, "target@roles.test", "targetpass1", domain.RoleMember, "")
	adminToken := loginAndGetToken(t, ts, admin.Email, "adminpass1")

	steps := []struct {
		name  string
		token string
		role  string
		want  int
	}{
		{"owner sets viewer", ownerToken, "viewer", http.StatusOK},
		{"owner sets member", ownerToken, "member", http.StatusOK},
		{"owner sets admin", ownerToken, "admin", http.StatusOK},
		{"admin demotes admin to viewer", adminToken, "viewer", http.StatusOK},
		{"admin sets member", adminToken, "member", http.StatusOK},
		{"admin cannot grant admin", adminToken, "admin", http.StatusForbidden},
		{"owner cannot set owner by role change", ownerToken, "owner", http.StatusForbidden},
		{"unknown role rejected", ownerToken, "superuser", http.StatusBadRequest},
	}
	for _, st := range steps {
		if code := patchUser(t, ts, st.token, target.ID, map[string]any{"role": st.role}); code != st.want {
			t.Fatalf("%s: expected %d, got %d", st.name, st.want, code)
		}
	}
	got, err := s.Users().GetByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != domain.RoleMember {
		t.Fatalf("expected the rejected changes to leave role member, got %s", got.Role)
	}
}
