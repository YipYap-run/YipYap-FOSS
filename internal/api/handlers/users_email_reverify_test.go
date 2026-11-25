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
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
)

// TestChangeEmailClearsVerification checks that, when a mailer is configured,
// PUT /auth/email only starts a pending change: the account keeps its current
// verified address until the link mailed to the new one is used (auth-13).
func TestChangeEmailClearsVerification(t *testing.T) {
	// Register through the no-mailer server so the owner starts verified.
	plain, s := setupTestServer(t)
	selfServiceRegisterAndGetToken(t, plain, "ReverifyOrg", "reverify@contact.test", "reverifypass1")

	// Second server on the same store with a mailer. Sends go to a closed
	// port and fail in the background, which the handler tolerates.
	m := mailer.New(mailer.Config{Host: "127.0.0.1", Port: 1})
	jwt := auth.NewJWTIssuer([]byte("test-secret-key-for-testing"), time.Hour)
	ts := httptest.NewServer(api.NewServer(s, jwt, bus.NewChannel(), nil, nil, "", "", api.ServerOptions{Mailer: m}))
	t.Cleanup(ts.Close)

	token := loginAndGetToken(t, ts, "reverify@contact.test", "reverifypass1")

	b, _ := json.Marshal(map[string]string{"current_password": "reverifypass1", "new_email": "moved@contact.test"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var out struct {
		Status       string `json:"status"`
		PendingEmail string `json:"pending_email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "confirmation_sent" || out.PendingEmail != "moved@contact.test" {
		t.Fatalf("response should start a pending change: %+v", out)
	}

	got, err := s.Users().GetByEmail(context.Background(), "reverify@contact.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailVerifiedAt == nil {
		t.Fatal("the current address must stay verified while a change is pending")
	}
	if got.PendingEmail != "moved@contact.test" || got.PendingEmailNonce == "" {
		t.Fatalf("pending change not stored: %+v", got)
	}
	if _, err := s.Users().GetByEmail(context.Background(), "moved@contact.test"); err == nil {
		t.Fatal("the new address must not become the account email before confirmation")
	}

	// The pending address cannot be used to sign in.
	lb, _ := json.Marshal(map[string]string{"email": "moved@contact.test", "password": "reverifypass1"})
	lresp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(lb))
	if err != nil {
		t.Fatal(err)
	}
	_ = lresp.Body.Close()
	if lresp.StatusCode != http.StatusUnauthorized && lresp.StatusCode != http.StatusForbidden {
		t.Fatalf("login with a pending unproven address: expected 401 or 403, got %d", lresp.StatusCode)
	}
}

// TestChangeEmailWithoutMailerStaysVerified pins the no-SMTP behavior: there
// is no way to verify, so the change applies and the account stays usable.
func TestChangeEmailWithoutMailerStaysVerified(t *testing.T) {
	ts, s := setupTestServer(t)
	token := selfServiceRegisterAndGetToken(t, ts, "NoMailOrg", "nomail@contact.test", "nomailpass1")

	b, _ := json.Marshal(map[string]string{"current_password": "nomailpass1", "new_email": "nomail2@contact.test"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/auth/email", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	got, err := s.Users().GetByEmail(context.Background(), "nomail2@contact.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailVerifiedAt == nil {
		t.Fatal("account should stay verified when no mailer is configured")
	}
	loginAndGetToken(t, ts, "nomail2@contact.test", "nomailpass1")
}
