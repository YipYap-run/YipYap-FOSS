package auth

import (
	"testing"
	"time"
)

func TestJWT_IssueAndValidate(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 15*time.Minute)

	userID := "user-123"
	orgID := "org-456"
	role := "admin"

	tokenStr, err := issuer.Issue(userID, orgID, role)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if tokenStr == "" {
		t.Fatal("Issue() returned empty token")
	}

	claims, err := issuer.Validate(tokenStr)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("UserID = %q, want %q", claims.UserID, userID)
	}
	if claims.OrgID != orgID {
		t.Errorf("OrgID = %q, want %q", claims.OrgID, orgID)
	}
	if claims.Role != role {
		t.Errorf("Role = %q, want %q", claims.Role, role)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), -1*time.Minute)

	tokenStr, err := issuer.Issue("user-123", "org-456", "admin")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	_, err = issuer.Validate(tokenStr)
	if err == nil {
		t.Error("Validate() with expired token: expected error, got nil")
	}
}

func TestIssueAndValidatePasswordReset(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	token, err := issuer.IssuePasswordReset("u1", "org1", "user@example.com", "$2a$12$dummyhashfortesting000000000000000000000000000000")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	claims, err := issuer.ValidatePasswordReset(token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("expected user_id u1, got %s", claims.UserID)
	}
	if claims.Role != "user@example.com" {
		t.Errorf("expected email in role, got %s", claims.Role)
	}
}

func TestValidatePasswordReset_RejectsSessionToken(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	token, _ := issuer.Issue("u1", "org1", "owner")
	_, err := issuer.ValidatePasswordReset(token)
	if err == nil {
		t.Fatal("expected error validating session token as password-reset")
	}
}

func TestValidatePasswordReset_RejectsMFAToken(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	token, _ := issuer.IssueMFA("u1", "org1", "owner")
	_, err := issuer.ValidatePasswordReset(token)
	if err == nil {
		t.Fatal("expected error validating MFA token as password-reset")
	}
}
