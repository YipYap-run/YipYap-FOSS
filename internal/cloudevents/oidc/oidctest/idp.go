// Package oidctest provides a minimal in-process OIDC Identity Provider
// for use by tests in other packages that need to exercise JWT-bearer
// verification. It serves an OIDC discovery document and a matching JWKS
// signed by a single RSA key.
//
// This helper is test-only, it lives outside _test.go files so that it
// can be imported by tests in packages other than internal/cloudevents/oidc
// (for example, internal/api/handlers where we verify ingest-endpoint
// behaviour end-to-end). It is not wired into production code.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// IdP is a minimal httptest-backed OIDC provider with a single RSA signing key.
type IdP struct {
	Server  *httptest.Server
	KeyID   string
	Key     *rsa.PrivateKey
	JWKSURL string

	// discoveryHits counts requests to /.well-known/openid-configuration.
	discoveryHits int64
	// jwksHits counts requests to /jwks.json.
	jwksHits int64
}

// NewIdP spins up a local OIDC provider with one RSA signing key. The
// server is torn down automatically via t.Cleanup.
func NewIdP(t *testing.T) *IdP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}

	idp := &IdP{
		KeyID: "test-key-1",
		Key:   key,
	}

	mux := http.NewServeMux()
	var baseURL string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&idp.discoveryHits, 1)
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
		atomic.AddInt64(&idp.jwksHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksDoc(key, idp.KeyID))
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	t.Cleanup(srv.Close)

	idp.Server = srv
	idp.JWKSURL = srv.URL + "/jwks.json"
	return idp
}

// Issuer returns the IdP's issuer URL (equivalent to Server.URL).
func (i *IdP) Issuer() string {
	return i.Server.URL
}

// DiscoveryHits returns the number of times /.well-known/openid-configuration
// has been requested.
func (i *IdP) DiscoveryHits() int {
	return int(atomic.LoadInt64(&i.discoveryHits))
}

// JWKSHits returns the number of times /jwks.json has been requested.
func (i *IdP) JWKSHits() int {
	return int(atomic.LoadInt64(&i.jwksHits))
}

// SignJWT produces an RS256-signed JWT from the given claims using the
// IdP's signing key.
func (i *IdP) SignJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	return SignJWT(t, i.Key, i.KeyID, claims)
}

// SignJWT produces an RS256-signed JWT from claims, keyed with kid.
func SignJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
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

// StdClaims returns a set of claims that should verify successfully
// against an IdP whose issuer is iss, for audience aud.
func StdClaims(iss, sub, aud string, now time.Time) map[string]any {
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
