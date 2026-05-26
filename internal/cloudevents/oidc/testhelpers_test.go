package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testIdP is a minimal httptest.Server that serves an OIDC discovery
// document and a matching JWKS endpoint signed by a single RSA key.
type testIdP struct {
	Server  *httptest.Server
	KeyID   string
	Key     *rsa.PrivateKey
	JWKSURL string
}

// newTestIdP spins up a local OIDC provider with one RSA signing key.
func newTestIdP(t *testing.T) *testIdP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}

	mux := http.NewServeMux()
	var baseURL string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                baseURL,
			"jwks_uri":                              baseURL + "/jwks.json",
			"authorization_endpoint":                baseURL + "/authorize",
			"token_endpoint":                        baseURL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})

	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksDoc(key, "test-key-1"))
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL

	t.Cleanup(srv.Close)

	return &testIdP{
		Server:  srv,
		KeyID:   "test-key-1",
		Key:     key,
		JWKSURL: srv.URL + "/jwks.json",
	}
}

// jwksDoc builds a JWKS JSON document advertising the public half of key
// under keyID using RS256.
func jwksDoc(key *rsa.PrivateKey, keyID string) map[string]any {
	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": keyID,
				"n":   b64url(key.N.Bytes()),
				"e":   b64url(big.NewInt(int64(key.E)).Bytes()),
			},
		},
	}
}

// b64url returns base64url-no-padding encoding as required by RFC 7517.
func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// signJWT produces an RS256-signed JWT from claims using key with the given kid.
func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	mc := jwt.MapClaims{}
	for k, v := range claims {
		mc[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, mc)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

// stdClaims returns a set of claims that should verify successfully
// against an IdP whose issuer is iss, for audience aud.
func stdClaims(iss, sub, aud string, now time.Time) map[string]any {
	return map[string]any{
		"iss": iss,
		"sub": sub,
		"aud": aud,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"jti": fmt.Sprintf("jti-%d", now.UnixNano()),
	}
}
