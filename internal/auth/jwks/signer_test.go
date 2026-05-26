package jwks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func mustGenerateAndActivate(t *testing.T, alg Algorithm) (*Manager, *SigningKey) {
	t.Helper()
	m, _ := newTestManager(t, nil)
	k, err := m.Generate(alg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := m.SaveAndActivate(context.Background(), k); err != nil {
		t.Fatalf("SaveAndActivate: %v", err)
	}
	return m, k
}

func roundTrip(t *testing.T, alg Algorithm) {
	t.Helper()
	m, k := mustGenerateAndActivate(t, alg)

	signer, err := m.GetActiveSigner(context.Background())
	if err != nil {
		t.Fatalf("GetActiveSigner: %v", err)
	}
	if signer.KID() != k.KID {
		t.Fatalf("kid mismatch: signer=%s key=%s", signer.KID(), k.KID)
	}

	now := time.Now().UTC().Truncate(time.Second)
	tok, err := signer.Sign(Claims{
		Issuer:    "https://yipyap.example/",
		Subject:   "test-subject",
		Audience:  []string{"aud-1"},
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
		NotBefore: now,
		JTI:       "jti-1",
		Extra:     map[string]any{"tenant": "org-1"},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Parse the JWT with go-jose's verifier using the JWK we stored.
	var jwk jose.JSONWebKey
	if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
		t.Fatalf("decode public jwk: %v", err)
	}

	parsed, err := jwt.ParseSigned(tok, allowedAlgs())
	if err != nil {
		t.Fatalf("ParseSigned: %v", err)
	}
	var out jwt.Claims
	var extras map[string]any
	if err := parsed.Claims(jwk.Key, &out, &extras); err != nil {
		t.Fatalf("Claims: %v", err)
	}
	if out.Issuer != "https://yipyap.example/" {
		t.Fatalf("iss: %v", out.Issuer)
	}
	if out.Subject != "test-subject" {
		t.Fatalf("sub: %v", out.Subject)
	}
	if len(out.Audience) != 1 || out.Audience[0] != "aud-1" {
		t.Fatalf("aud: %v", out.Audience)
	}
	if extras["tenant"] != "org-1" {
		t.Fatalf("extras: %v", extras)
	}
}

func TestSigner_Sign_EdDSA_RoundTrip(t *testing.T) { roundTrip(t, EdDSA) }
func TestSigner_Sign_RS256_RoundTrip(t *testing.T) { roundTrip(t, RS256) }
func TestSigner_Sign_RS384_RoundTrip(t *testing.T) { roundTrip(t, RS384) }
func TestSigner_Sign_RS512_RoundTrip(t *testing.T) { roundTrip(t, RS512) }
func TestSigner_Sign_PS256_RoundTrip(t *testing.T) { roundTrip(t, PS256) }
func TestSigner_Sign_PS384_RoundTrip(t *testing.T) { roundTrip(t, PS384) }
func TestSigner_Sign_PS512_RoundTrip(t *testing.T) { roundTrip(t, PS512) }
func TestSigner_Sign_ES256_RoundTrip(t *testing.T) { roundTrip(t, ES256) }
func TestSigner_Sign_ES384_RoundTrip(t *testing.T) { roundTrip(t, ES384) }
func TestSigner_Sign_ES512_RoundTrip(t *testing.T) { roundTrip(t, ES512) }

// TestSigner_Sign_RejectsRemovedAlgs asserts that algorithms intentionally
// excluded from the whitelist (symmetric HMAC, "none", random garbage) are
// rejected by both Generate and the joseAlgorithm mapping, defence in
// depth against alg-confusion attacks and future refactors that might try
// to pipe an Algorithm string through without revalidation.
func TestSigner_Sign_RejectsRemovedAlgs(t *testing.T) {
	rejected := []Algorithm{
		Algorithm("HS256"),
		Algorithm("HS384"),
		Algorithm("HS512"),
		Algorithm("none"),
		Algorithm(""),
		Algorithm("Ed448"),
		Algorithm("RS1024"),
	}
	m, _ := newTestManager(t, nil)
	for _, alg := range rejected {
		alg := alg
		t.Run(string(alg), func(t *testing.T) {
			if _, err := m.Generate(alg); err == nil {
				t.Fatalf("Generate(%q) expected error, got nil", alg)
			} else if !errors.Is(err, ErrUnsupportedAlg) {
				t.Fatalf("Generate(%q) expected ErrUnsupportedAlg, got %v", alg, err)
			}
			if _, err := joseAlgorithm(alg); err == nil {
				t.Fatalf("joseAlgorithm(%q) expected error, got nil", alg)
			} else if !errors.Is(err, ErrUnsupportedAlg) {
				t.Fatalf("joseAlgorithm(%q) expected ErrUnsupportedAlg, got %v", alg, err)
			}
			if alg.Valid() {
				t.Fatalf("Algorithm(%q).Valid() = true, expected false", alg)
			}
		})
	}
}

func TestSigner_Sign_IncludesKID(t *testing.T) {
	m, k := mustGenerateAndActivate(t, EdDSA)
	signer, err := m.GetActiveSigner(context.Background())
	if err != nil {
		t.Fatalf("GetActiveSigner: %v", err)
	}
	now := time.Now().UTC()
	tok, err := signer.Sign(Claims{Issuer: "iss", Subject: "sub", Audience: []string{"a"}, IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Compact serialization: header.payload.signature; base64url decode header.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hobj map[string]any
	if err := json.Unmarshal(hdr, &hobj); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if hobj["kid"] != k.KID {
		t.Fatalf("kid in header: got %v want %v", hobj["kid"], k.KID)
	}
	if hobj["typ"] != "JWT" {
		t.Fatalf("typ: %v", hobj["typ"])
	}
	// Critical: never "none".
	if hobj["alg"] == "none" {
		t.Fatalf("alg must never be none")
	}
}

func TestSigner_Sign_RefusesNoneAlg(t *testing.T) {
	// The Signer pipeline never permits "none": go-jose.NewSigner rejects
	// an unknown algorithm, and Generate refuses to construct a
	// SigningKey with Algorithm=="none". Verify both behaviours.
	m, _ := newTestManager(t, nil)
	if _, err := m.Generate(Algorithm("none")); err == nil {
		t.Fatalf("Generate(none) expected error")
	}
	// Directly probe joseAlgorithm / newSigner too.
	if _, err := joseAlgorithm(Algorithm("none")); err == nil {
		t.Fatalf("joseAlgorithm(none) expected error")
	}
}

// allowedAlgs lists the algorithms jwt.ParseSigned is permitted to see.
// go-jose/v4 requires an explicit allowlist to mitigate alg-confusion.
func allowedAlgs() []jose.SignatureAlgorithm {
	return []jose.SignatureAlgorithm{
		jose.EdDSA,
		jose.RS256, jose.RS384, jose.RS512,
		jose.PS256, jose.PS384, jose.PS512,
		jose.ES256, jose.ES384, jose.ES512,
	}
}
