package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifier_Verify_ValidToken(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	now := time.Now()
	raw := signJWT(t, idp.Key, idp.KeyID, stdClaims(idp.Server.URL, "user-42", "alerts-ingest", now))

	tok, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.Subject != "user-42" {
		t.Errorf("Subject = %q, want user-42", tok.Subject)
	}
	if tok.Issuer != idp.Server.URL {
		t.Errorf("Issuer = %q, want %q", tok.Issuer, idp.Server.URL)
	}
	if len(tok.Audience) != 1 || tok.Audience[0] != "alerts-ingest" {
		t.Errorf("Audience = %v, want [alerts-ingest]", tok.Audience)
	}
	if tok.Expiry.IsZero() {
		t.Error("Expiry zero, want non-zero")
	}
	if tok.Raw == nil || tok.Raw["sub"] != "user-42" {
		t.Errorf("Raw claims missing sub: %v", tok.Raw)
	}
}

func TestVerifier_Verify_WrongIssuer(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	now := time.Now()
	claims := stdClaims(idp.Server.URL, "user-42", "alerts-ingest", now)
	claims["iss"] = "https://attacker.example"
	raw := signJWT(t, idp.Key, idp.KeyID, claims)

	_, err := v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrWrongIssuer) {
		t.Fatalf("err = %v, want ErrWrongIssuer", err)
	}
}

func TestVerifier_Verify_WrongAudience(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	now := time.Now()
	raw := signJWT(t, idp.Key, idp.KeyID, stdClaims(idp.Server.URL, "user-42", "wrong-aud", now))

	_, err := v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrWrongAudience) {
		t.Fatalf("err = %v, want ErrWrongAudience", err)
	}
}

func TestVerifier_Verify_MultipleAudiences_AnyMatches(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"a", "b", "c"},
	})

	now := time.Now()
	raw := signJWT(t, idp.Key, idp.KeyID, stdClaims(idp.Server.URL, "user-42", "b", now))

	tok, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.Subject != "user-42" {
		t.Errorf("Subject = %q, want user-42", tok.Subject)
	}
}

func TestVerifier_Verify_Expired(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	past := time.Now().Add(-10 * time.Minute)
	claims := stdClaims(idp.Server.URL, "user-42", "alerts-ingest", past)
	// Override exp to ensure it's in the past.
	claims["exp"] = past.Add(1 * time.Minute).Unix()
	raw := signJWT(t, idp.Key, idp.KeyID, claims)

	_, err := v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestVerifier_Verify_InvalidSignature(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	// Sign with a different key than the one advertised by the JWKS.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	now := time.Now()
	raw := signJWT(t, otherKey, idp.KeyID, stdClaims(idp.Server.URL, "user-42", "alerts-ingest", now))

	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}
}

func TestVerifier_Verify_MalformedToken(t *testing.T) {
	idp := newTestIdP(t)
	v := NewVerifier(VerifierConfig{
		Issuer:    idp.Server.URL,
		Audiences: []string{"alerts-ingest"},
	})

	_, err := v.Verify(context.Background(), "not.a.jwt")
	if !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("err = %v, want ErrMalformedToken", err)
	}
}

func TestVerifier_Verify_JWKSUnavailable(t *testing.T) {
	// An issuer whose discovery endpoint 500s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	v := NewVerifier(VerifierConfig{
		Issuer:    srv.URL,
		Audiences: []string{"alerts-ingest"},
	})

	// Any syntactically-valid JWT will do; we expect JWKS fetch to fail
	// before signature verification. Use a minimal token signed with a
	// throwaway key, the signing attempt is cheap.
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	raw := signJWT(t, k, "k", stdClaims(srv.URL, "u", "alerts-ingest", time.Now()))

	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("err = %v, want ErrJWKSUnavailable", err)
	}
}

func TestVerifier_Verify_DirectJWKSURL(t *testing.T) {
	idp := newTestIdP(t)

	// Point at a made-up issuer string, we expect the iss claim to be
	// compared against this, and JWKSURL to bypass discovery entirely.
	issuer := "https://my-issuer.example"
	v := NewVerifier(VerifierConfig{
		Issuer:    issuer,
		JWKSURL:   idp.JWKSURL,
		Audiences: []string{"alerts-ingest"},
	})

	now := time.Now()
	raw := signJWT(t, idp.Key, idp.KeyID, stdClaims(issuer, "user-42", "alerts-ingest", now))

	tok, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.Subject != "user-42" {
		t.Errorf("Subject = %q, want user-42", tok.Subject)
	}
	if tok.Issuer != issuer {
		t.Errorf("Issuer = %q, want %q", tok.Issuer, issuer)
	}
}
