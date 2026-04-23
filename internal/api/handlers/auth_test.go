package handlers_test

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
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func setupTestServer(t *testing.T) (*httptest.Server, *sqlite.SQLiteStore) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), 1*time.Hour)
	handler := api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{RegistrationEnabled: true})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return ts, s
}

func TestRegisterAndLogin(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Register.
	regBody, _ := json.Marshal(map[string]string{
		"org_name": "Test Org",
		"email":    "admin@test.com",
		"password": "supersecret123",
	})
	resp, err := http.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", resp.StatusCode)
	}

	var regResp struct {
		Token string       `json:"token"`
		User  *domain.User `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatal(err)
	}
	if regResp.Token == "" {
		t.Fatal("register: expected non-empty token")
	}
	if regResp.User.Email != "admin@test.com" {
		t.Fatalf("register: expected email admin@test.com, got %s", regResp.User.Email)
	}
	if regResp.User.Role != domain.RoleOwner {
		t.Fatalf("register: expected role owner, got %s", regResp.User.Role)
	}

	// Login.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "admin@test.com",
		"password": "supersecret123",
	})
	resp2, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", resp2.StatusCode)
	}

	var loginResp struct {
		Token string       `json:"token"`
		User  *domain.User `json:"user"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&loginResp); err != nil {
		t.Fatal(err)
	}
	if loginResp.Token == "" {
		t.Fatal("login: expected non-empty token")
	}

	// Use token to GET /api/v1/org.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/org", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("get org: expected 200, got %d", resp3.StatusCode)
	}

	var org domain.Org
	if err := json.NewDecoder(resp3.Body).Decode(&org); err != nil {
		t.Fatal(err)
	}
	if org.Name != "Test Org" {
		t.Fatalf("get org: expected name 'Test Org', got %q", org.Name)
	}
	if org.Slug != "test-org" {
		t.Fatalf("get org: expected slug 'test-org', got %q", org.Slug)
	}
}

func TestRegisterValidation(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Missing fields.
	body, _ := json.Marshal(map[string]string{
		"email": "admin@test.com",
	})
	resp, err := http.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Register first.
	regBody, _ := json.Marshal(map[string]string{
		"org_name": "Test Org",
		"email":    "admin@test.com",
		"password": "supersecret123",
	})
	resp, _ := http.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(regBody))
	_ = resp.Body.Close()

	// Login with wrong password.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "admin@test.com",
		"password": "wrongpassword",
	})
	resp2, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/org")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
