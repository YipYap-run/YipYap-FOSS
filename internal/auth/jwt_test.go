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

func TestIssueAndValidateAccountDeletion(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	token, err := issuer.IssueAccountDeletion("u1", "org1", "user@example.com", "$2a$12$dummyhashfortesting000000000000000000000000000000")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	claims, err := issuer.ValidateAccountDeletion(token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("expected user_id u1, got %s", claims.UserID)
	}
	if claims.Role != "user@example.com" {
		t.Errorf("expected email in role, got %s", claims.Role)
	}
	if claims.Nonce == "" {
		t.Error("expected non-empty nonce")
	}
}

func TestValidateAccountDeletion_RejectsOtherTokens(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)

	// Session token should be rejected
	session, _ := issuer.Issue("u1", "org1", "owner")
	if _, err := issuer.ValidateAccountDeletion(session); err == nil {
		t.Fatal("expected error validating session token as account-delete")
	}

	// Password-reset token should be rejected
	reset, _ := issuer.IssuePasswordReset("u1", "org1", "a@b.com", "$2a$12$dummyhash")
	if _, err := issuer.ValidateAccountDeletion(reset); err == nil {
		t.Fatal("expected error validating password-reset token as account-delete")
	}
}

func TestIssueAndValidateAccountRecovery(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	token, err := issuer.IssueAccountRecovery("u1", "org1", "user@example.com", "$2a$12$dummyhashfortesting000000000000000000000000000000")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	claims, err := issuer.ValidateAccountRecovery(token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("expected user_id u1, got %s", claims.UserID)
	}
	if claims.Role != "user@example.com" {
		t.Errorf("expected email in role, got %s", claims.Role)
	}
	if claims.Nonce == "" {
		t.Error("expected non-empty nonce")
	}
}

func TestValidateAccountDeletion_RejectsRecoveryToken(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)
	rec, err := issuer.IssueAccountRecovery("u1", "org1", "a@b.com", "$2a$12$dummyhashvalue")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.ValidateAccountDeletion(rec); err == nil {
		t.Fatal("expected error validating account-recover token as account-delete")
	}
}

func TestValidateAccountRecovery_RejectsOtherTokens(t *testing.T) {
	issuer := NewJWTIssuer([]byte("test-secret"), 24*time.Hour)

	// Account-delete token should be rejected
	del, _ := issuer.IssueAccountDeletion("u1", "org1", "a@b.com", "$2a$12$dummyhash")
	if _, err := issuer.ValidateAccountRecovery(del); err == nil {
		t.Fatal("expected error validating account-delete token as account-recover")
	}

	// Session token should be rejected
	session, _ := issuer.Issue("u1", "org1", "owner")
	if _, err := issuer.ValidateAccountRecovery(session); err == nil {
		t.Fatal("expected error validating session token as account-recover")
	}
}
