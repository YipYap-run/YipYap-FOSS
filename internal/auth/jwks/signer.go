package jwks

import (
	"fmt"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Claims are the standard JWT claims the Signer knows how to serialise.
// Extras may carry additional custom claims (e.g. `tenant_id`, `kind`).
type Claims struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	JTI       string
	Extra     map[string]any
}

// Signer mints JWTs using a single signing key. It is safe for concurrent
// use, go-jose's Signer is stateless per-call; we only wrap it.
type Signer struct {
	key   *SigningKey
	joseS jose.Signer
	obs   Observer
	mu    sync.Mutex // guards a per-signer serialisation budget if we add rate limiting later
}

func newSigner(k *SigningKey) (*Signer, error) {
	alg, err := joseAlgorithm(k.Algorithm)
	if err != nil {
		return nil, err
	}
	opts := (&jose.SignerOptions{}).
		WithType("JWT").
		WithHeader(jose.HeaderKey("kid"), k.KID)

	s, err := jose.NewSigner(jose.SigningKey{
		Algorithm: alg,
		Key:       k.Private,
	}, opts)
	if err != nil {
		return nil, fmt.Errorf("jwks: new signer for kid %s: %w", k.KID, err)
	}
	return &Signer{key: k, joseS: s}, nil
}

// KID returns the key identifier this signer uses. Consumers include it
// in OIDC discovery metadata or diagnostic logs.
func (s *Signer) KID() string { return s.key.KID }

// Algorithm returns the signer's algorithm.
func (s *Signer) Algorithm() Algorithm { return s.key.Algorithm }

// Sign serialises claims into a compact JWS. Standard claims are filled
// from the struct fields; Extras are merged last so they may override
// reserved claim names (caller's responsibility not to misuse this).
func (s *Signer) Sign(c Claims) (string, error) {
	standard := jwt.Claims{
		Issuer:   c.Issuer,
		Subject:  c.Subject,
		Audience: jwt.Audience(c.Audience),
		ID:       c.JTI,
	}
	if !c.ExpiresAt.IsZero() {
		standard.Expiry = jwt.NewNumericDate(c.ExpiresAt)
	}
	if !c.IssuedAt.IsZero() {
		standard.IssuedAt = jwt.NewNumericDate(c.IssuedAt)
	}
	if !c.NotBefore.IsZero() {
		standard.NotBefore = jwt.NewNumericDate(c.NotBefore)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b := jwt.Signed(s.joseS).Claims(standard)
	if len(c.Extra) > 0 {
		b = b.Claims(c.Extra)
	}
	tok, err := b.Serialize()
	if err != nil {
		return "", fmt.Errorf("jwks: sign: %w", err)
	}
	if s.obs != nil {
		s.obs.OnSign(s.key.Algorithm, s.key.KID)
	}
	return tok, nil
}

// joseAlgorithm maps our Algorithm to the go-jose constant. Returning an
// error on an unknown value is defensive; Valid() should catch this at
// key-generation time.
func joseAlgorithm(a Algorithm) (jose.SignatureAlgorithm, error) {
	switch a {
	case EdDSA:
		return jose.EdDSA, nil
	case RS256:
		return jose.RS256, nil
	case RS384:
		return jose.RS384, nil
	case RS512:
		return jose.RS512, nil
	case PS256:
		return jose.PS256, nil
	case PS384:
		return jose.PS384, nil
	case PS512:
		return jose.PS512, nil
	case ES256:
		return jose.ES256, nil
	case ES384:
		return jose.ES384, nil
	case ES512:
		return jose.ES512, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlg, a)
	}
}
