package jwks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// TestE2E_SignAndVerifyViaJWKSHandler exercises the full round-trip for
// each algorithm: generate+activate a key, mint a JWT via Signer, fetch
// the JWK Set from the HTTP handler, and verify the JWT against the
// served keys using go-jose.
func TestE2E_SignAndVerifyViaJWKSHandler(t *testing.T) {
	for _, alg := range []Algorithm{EdDSA, RS256, ES256} {
		alg := alg
		t.Run(string(alg), func(t *testing.T) {
			m, _ := newTestManager(t, nil)
			ctx := context.Background()

			k, err := m.Generate(alg)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if err := m.SaveAndActivate(ctx, k); err != nil {
				t.Fatalf("SaveAndActivate: %v", err)
			}

			signer, err := m.GetActiveSigner(ctx)
			if err != nil {
				t.Fatalf("GetActiveSigner: %v", err)
			}

			now := time.Now().UTC().Truncate(time.Second)
			tok, err := signer.Sign(Claims{
				Issuer:    "https://yipyap.example/",
				Subject:   "ce:alert:abc",
				Audience:  []string{"knative-source"},
				IssuedAt:  now,
				ExpiresAt: now.Add(DefaultTokenTTL),
				JTI:       "jti-e2e",
			})
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}

			// Serve + fetch JWKS.
			srv := httptest.NewServer(NewJWKSHandler(m))
			defer srv.Close()

			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatalf("GET JWKS: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 200 {
				t.Fatalf("JWKS status: %d", resp.StatusCode)
			}

			var js jose.JSONWebKeySet
			if err := json.NewDecoder(resp.Body).Decode(&js); err != nil {
				t.Fatalf("decode JWKS: %v", err)
			}
			if len(js.Keys) != 1 {
				t.Fatalf("expected 1 key in set, got %d", len(js.Keys))
			}
			if js.Keys[0].KeyID != k.KID {
				t.Fatalf("kid mismatch: served=%s key=%s", js.Keys[0].KeyID, k.KID)
			}

			parsed, err := jwt.ParseSigned(tok, []jose.SignatureAlgorithm{joseAlgMust(alg)})
			if err != nil {
				t.Fatalf("ParseSigned: %v", err)
			}
			var out jwt.Claims
			if err := parsed.Claims(js.Keys[0].Key, &out); err != nil {
				t.Fatalf("verify against served JWKS: %v", err)
			}
			if out.Subject != "ce:alert:abc" {
				t.Fatalf("sub: %v", out.Subject)
			}
		})
	}
}

func joseAlgMust(a Algorithm) jose.SignatureAlgorithm {
	j, err := joseAlgorithm(a)
	if err != nil {
		panic(err)
	}
	return j
}
