package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// seedOrgUser inserts a verified user straight into the store so the test
// does not depend on how the members API creates accounts.
func seedOrgUser(t *testing.T, s *sqlite.SQLiteStore, orgID, email, password string, role domain.UserRole, phone string) *domain.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	u := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        orgID,
		Email:        email,
		Name:         email,
		PasswordHash: hash,
		Role:         role,
		Phone:        phone,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	ctx := context.Background()
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().MarkEmailVerified(ctx, u.ID, now); err != nil {
		t.Fatal(err)
	}
	return u
}

func patchUser(t *testing.T, ts *httptest.Server, token, id string, body map[string]any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/users/"+id, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestUpdateUserContactDetails(t *testing.T) {
	ts, s := setupTestServer(t)
	ctx := context.Background()

	ownerToken := selfServisignupAndGetToken(t, ts, "ContactOrg", "owner@contact.test", "ownerpass1")
	owner, err := s.Users().GetByEmail(ctx, "owner@contact.test")
	if err != nil {
		t.Fatal(err)
	}
	// Give the owner a phone so we can check it is left alone.
	owner.Phone = "+1 555 010 0001"
	if err := s.Users().Update(ctx, owner); err != nil {
		t.Fatal(err)
	}

	admin := seedOrgUser(t, s, owner.OrgID, "admin@contact.test", "adminpass1", domain.RoleAdmin, "")
	member := seedOrgUser(t, s, owner.OrgID, "member@contact.test", "memberpass1", domain.RoleMember, "+1 555 010 0003")
	adminToken := loginAndGetToken(t, ts, admin.Email, "adminpass1")

	t.Run("admin cannot change owner email or phone", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, owner.ID, map[string]any{"email": "attacker@evil.test"}); code != http.StatusForbidden {
			t.Fatalf("owner email: expected 403, got %d", code)
		}
		if code := patchUser(t, ts, adminToken, owner.ID, map[string]any{"phone": "+1 555 666 7777"}); code != http.StatusForbidden {
			t.Fatalf("owner phone: expected 403, got %d", code)
		}
		got, err := s.Users().GetByID(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != "owner@contact.test" || got.Phone != "+1 555 010 0001" {
			t.Fatalf("owner row changed: email=%q phone=%q", got.Email, got.Phone)
		}
	})

	t.Run("admin cannot change member email or phone", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, member.ID, map[string]any{"email": "elsewhere@contact.test"}); code != http.StatusForbidden {
			t.Fatalf("member email: expected 403, got %d", code)
		}
		if code := patchUser(t, ts, adminToken, member.ID, map[string]any{"phone": "+1 555 666 7777"}); code != http.StatusForbidden {
			t.Fatalf("member phone: expected 403, got %d", code)
		}
		got, err := s.Users().GetByID(ctx, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != "member@contact.test" || got.Phone != "+1 555 010 0003" {
			t.Fatalf("member row changed: email=%q phone=%q", got.Email, got.Phone)
		}
	})

	t.Run("owner cannot change member email", func(t *testing.T) {
		if code := patchUser(t, ts, ownerToken, member.ID, map[string]any{"email": "elsewhere@contact.test"}); code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", code)
		}
	})

	t.Run("echoing current contact values is allowed", func(t *testing.T) {
		code := patchUser(t, ts, adminToken, member.ID, map[string]any{
			"email": "MEMBER@contact.test", "phone": "+1 555 010 0003", "role": "viewer",
		})
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		got, _ := s.Users().GetByID(ctx, member.ID)
		if got.Role != domain.RoleViewer || got.Email != "member@contact.test" {
			t.Fatalf("unexpected row: role=%s email=%s", got.Role, got.Email)
		}
	})

	t.Run("self phone edit is validated and saved", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, admin.ID, map[string]any{"phone": "call me"}); code != http.StatusBadRequest {
			t.Fatalf("invalid phone: expected 400, got %d", code)
		}
		if code := patchUser(t, ts, adminToken, admin.ID, map[string]any{"phone": "123"}); code != http.StatusBadRequest {
			t.Fatalf("short phone: expected 400, got %d", code)
		}
		if code := patchUser(t, ts, adminToken, admin.ID, map[string]any{"phone": " +1 (555) 010-0002 "}); code != http.StatusOK {
			t.Fatalf("valid phone: expected 200, got %d", code)
		}
		got, _ := s.Users().GetByID(ctx, admin.ID)
		if got.Phone != "+1 (555) 010-0002" {
			t.Fatalf("phone not saved: %q", got.Phone)
		}
	})

	t.Run("self email change must use the re-authenticated flow", func(t *testing.T) {
		if code := patchUser(t, ts, adminToken, admin.ID, map[string]any{"email": "admin2@contact.test"}); code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", code)
		}
		got, _ := s.Users().GetByID(ctx, admin.ID)
		if got.Email != "admin@contact.test" {
			t.Fatalf("email changed: %q", got.Email)
		}
	})

	t.Run("owner role still not assignable", func(t *testing.T) {
		if code := patchUser(t, ts, ownerToken, admin.ID, map[string]any{"role": "owner"}); code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", code)
		}
	})
}

func TestChangeEmailValidation(t *testing.T) {
	ts, _ := setupTestServer(t)
	token := selfServisignupAndGetToken(t, ts, "ValidateOrg", "valid@contact.test", "validpass1")
	selfServisignupAndGetToken(t, ts, "OtherOrg", "taken@contact.test", "otherpass1")

	put := func(email string) int {
		b, _ := json.Marshal(map[string]string{"current_password": "validpass1", "new_email": email})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, bad := range []string{"not-an-email", "Name <x@contact.test>", "a@b@c"} {
		if code := put(bad); code != http.StatusBadRequest {
			t.Fatalf("%q: expected 400, got %d", bad, code)
		}
	}
	if code := put("taken@contact.test"); code != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d", code)
	}
	if code := put(" fresh@contact.test "); code != http.StatusOK {
		t.Fatalf("valid: expected 200, got %d", code)
	}
}

// TestChangeEmailErrorContract pins the status codes and error strings that
// the SPA account settings form maps to user-facing copy (emailChangeError in
// web/src/pages/settings/account.jsx). Changing them breaks that mapping.
func TestChangeEmailErrorContract(t *testing.T) {
	ts, _ := setupTestServer(t)
	token := selfServisignupAndGetToken(t, ts, "ContractOrg", "contract@contact.test", "contractpass1")
	selfServisignupAndGetToken(t, ts, "ContractOther", "used@contact.test", "otherpass1")

	put := func(password, email string) (int, string) {
		b, _ := json.Marshal(map[string]string{"current_password": password, "new_email": email})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body.Error
	}

	cases := []struct {
		name, password, email string
		code                  int
		msg                   string
	}{
		{"invalid address", "contractpass1", "nope", http.StatusBadRequest, "enter a valid email address"},
		{"too long", "contractpass1", strings.Repeat("a", 250) + "@x.io", http.StatusBadRequest, "email address too long"},
		{"wrong password", "wrongpass", "new@contact.test", http.StatusUnauthorized, "invalid current password"},
		{"duplicate", "contractpass1", "used@contact.test", http.StatusConflict, "Another account already uses this email address."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, msg := put(c.password, c.email)
			if code != c.code || msg != c.msg {
				t.Fatalf("got %d %q, want %d %q", code, msg, c.code, c.msg)
			}
		})
	}
}
