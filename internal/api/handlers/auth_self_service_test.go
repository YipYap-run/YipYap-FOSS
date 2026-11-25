package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// selfServiceRegisterAndGetToken registers a new org+user and returns the bearer token.
func selfServiceRegisterAndGetToken(t *testing.T, ts *httptest.Server, orgName, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"org_name": orgName,
		"email":    email,
		"password": password,
	})
	resp, err := http.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", resp.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("register: expected non-empty token")
	}
	return result.Token
}

func TestChangePassword(t *testing.T) {
	ts, _ := setupTestServer(t)

	token := selfServiceRegisterAndGetToken(t, ts, "PwOrg", "pw@test.com", "oldpassword")

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"current_password": "oldpassword",
			"new_password":     "newpassword123",
		})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/password", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("change password: expected 200, got %d", resp.StatusCode)
		}

		var result struct {
			Token string       `json:"token"`
			User  *domain.User `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Token == "" {
			t.Fatal("change password: expected new token")
		}
		if result.User.ForcePasswordChange {
			t.Fatal("change password: ForcePasswordChange should be false after change")
		}

		// Verify login with new password works.
		loginBody, _ := json.Marshal(map[string]string{
			"email":    "pw@test.com",
			"password": "newpassword123",
		})
		resp2, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("login with new password: expected 200, got %d", resp2.StatusCode)
		}
	})

	t.Run("wrong current password", func(t *testing.T) {
		// Re-register a fresh user for this sub-test.
		token2 := selfServiceRegisterAndGetToken(t, ts, "PwOrg2", "pw2@test.com", "correctpass")

		body, _ := json.Marshal(map[string]string{
			"current_password": "wrongpass",
			"new_password":     "newpassword123",
		})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/password", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token2)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong password: expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("rejects a short or unchanged new password", func(t *testing.T) {
		token3 := selfServiceRegisterAndGetToken(t, ts, "PwOrg3", "pw3@test.com", "correctpass")
		for _, newPw := range []string{"short", "correctpass"} {
			body, _ := json.Marshal(map[string]string{
				"current_password": "correctpass",
				"new_password":     newPw,
			})
			req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/password", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token3)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("new password %q: expected 400, got %d", newPw, resp.StatusCode)
			}
		}
	})
}

func TestChangeEmail(t *testing.T) {
	ts, _ := setupTestServer(t)

	token := selfServiceRegisterAndGetToken(t, ts, "EmailOrg", "old@test.com", "mypassword")

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"current_password": "mypassword",
			"new_email":        "new@test.com",
		})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("change email: expected 200, got %d", resp.StatusCode)
		}

		var result struct {
			Token string       `json:"token"`
			User  *domain.User `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Token == "" {
			t.Fatal("change email: expected new token")
		}
		if result.User.Email != "new@test.com" {
			t.Fatalf("change email: expected new@test.com, got %s", result.User.Email)
		}

		// Verify login with new email works.
		loginBody, _ := json.Marshal(map[string]string{
			"email":    "new@test.com",
			"password": "mypassword",
		})
		resp2, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("login with new email: expected 200, got %d", resp2.StatusCode)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		token2 := selfServiceRegisterAndGetToken(t, ts, "EmailOrg2", "user2@test.com", "correctpass")

		body, _ := json.Marshal(map[string]string{
			"current_password": "wrongpass",
			"new_email":        "newemail2@test.com",
		})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token2)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong password: expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("email already taken", func(t *testing.T) {
		// Register two users; try to change second user's email to first user's email.
		selfServiceRegisterAndGetToken(t, ts, "EmailOrg3", "taken@test.com", "password1!")
		token4 := selfServiceRegisterAndGetToken(t, ts, "EmailOrg4", "other@test.com", "password2!")

		body, _ := json.Marshal(map[string]string{
			"current_password": "password2!",
			"new_email":        "taken@test.com",
		})
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token4)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("email taken: expected 409, got %d", resp.StatusCode)
		}
	})
}

// loginAndGetToken logs in with email/password and returns the bearer token.
func loginAndGetToken(t *testing.T, ts *httptest.Server, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Token string       `json:"token"`
		User  *domain.User `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Token
}

// createMember invites a member (no SMTP, so the response carries the
// accept link), accepts the invite with the given password and returns the
// new user.
func createMember(t *testing.T, ts *httptest.Server, ownerToken, email, password string) *domain.User {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "role": "member"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/users/invites", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite member: expected 201, got %d", resp.StatusCode)
	}
	var invited struct {
		AcceptURL string `json:"accept_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&invited); err != nil {
		t.Fatal(err)
	}
	_, token, ok := strings.Cut(invited.AcceptURL, "token=")
	if !ok {
		t.Fatalf("no accept link in invite response: %q", invited.AcceptURL)
	}
	acceptBody, _ := json.Marshal(map[string]string{"token": token, "password": password})
	resp2, err := http.Post(ts.URL+"/api/v1/invites/accept", "application/json", bytes.NewReader(acceptBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("accept invite: expected 201, got %d", resp2.StatusCode)
	}
	loginToken := loginAndGetToken(t, ts, email, password)
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp3.Body.Close() }()
	var list struct {
		Users []domain.User `json:"users"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	for i := range list.Users {
		if list.Users[i].Email == email {
			return &list.Users[i]
		}
	}
	t.Fatalf("member %s not listed after accepting the invite", email)
	return nil
}

func TestAdminResetPassword(t *testing.T) {
	t.Run("owner resets member password", func(t *testing.T) {
		ts, _ := setupTestServer(t)

		ownerToken := selfServiceRegisterAndGetToken(t, ts, "ResetOrg", "owner@reset.com", "ownerpass")
		member := createMember(t, ts, ownerToken, "member@reset.com", "memberpass")

		// Reset the member's password.
		body, _ := json.Marshal(map[string]string{"temporary_password": "temppass123"})
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/users/%s/reset-password", ts.URL, member.ID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reset password: expected 200, got %d", resp.StatusCode)
		}

		// Verify member can log in with the temporary password.
		loginBody, _ := json.Marshal(map[string]string{
			"email":    "member@reset.com",
			"password": "temppass123",
		})
		resp2, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("login with temp password: expected 200, got %d", resp2.StatusCode)
		}

		var loginResult struct {
			Token string       `json:"token"`
			User  *domain.User `json:"user"`
		}
		if err := json.NewDecoder(resp2.Body).Decode(&loginResult); err != nil {
			t.Fatal(err)
		}
		if !loginResult.User.ForcePasswordChange {
			t.Fatal("reset password: expected force_password_change to be true after admin reset")
		}
	})

	t.Run("member cannot reset passwords", func(t *testing.T) {
		ts, _ := setupTestServer(t)

		ownerToken := selfServiceRegisterAndGetToken(t, ts, "ResetOrg2", "owner2@reset.com", "ownerpass")
		_ = createMember(t, ts, ownerToken, "member2@reset.com", "memberpass")
		member2 := createMember(t, ts, ownerToken, "member3@reset.com", "memberpass2")

		// Log in as the first member.
		memberToken := loginAndGetToken(t, ts, "member2@reset.com", "memberpass")

		// Member tries to reset another user's password.
		body, _ := json.Marshal(map[string]string{"temporary_password": "hackedpass"})
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/users/%s/reset-password", ts.URL, member2.ID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+memberToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("member reset: expected 403, got %d", resp.StatusCode)
		}
	})
}
