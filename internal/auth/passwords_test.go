package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword() returned empty hash")
	}
	if hash == password {
		t.Fatal("HashPassword() returned plaintext password")
	}

	if err := VerifyPassword(hash, password); err != nil {
		t.Errorf("VerifyPassword() with correct password: error = %v", err)
	}

	if err := VerifyPassword(hash, "wrong-password"); err == nil {
		t.Error("VerifyPassword() with wrong password: expected error, got nil")
	}
}
