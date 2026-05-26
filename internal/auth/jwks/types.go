package jwks

import (
	"errors"
	"time"
)

// Algorithm names the supported JWS algorithms. The whitelist is
// deliberately asymmetric-only: symmetric HMAC variants (HS256/384/512)
// would leak a shared secret through any public JWKS document, and the
// unauthenticated "none" alg is never acceptable. Post-quantum
// algorithms are not yet standardised in JWA.
//
// EdDSA in this codebase implies Ed25519; go-jose/v4 does not support
// Ed448 and we do not pull in a third-party curve implementation.
type Algorithm string

const (
	// EdDSA signs with Ed25519 (RFC 8037).
	EdDSA Algorithm = "EdDSA"

	// RSASSA-PKCS#1 v1.5 family. All use a 2048-bit RSA key; the suffix
	// denotes the digest.
	RS256 Algorithm = "RS256"
	RS384 Algorithm = "RS384"
	RS512 Algorithm = "RS512"

	// RSASSA-PSS family. Same 2048-bit RSA key material as RS*; PSS
	// padding is the modern recommendation for RSA signatures.
	PS256 Algorithm = "PS256"
	PS384 Algorithm = "PS384"
	PS512 Algorithm = "PS512"

	// ECDSA family. The digest-size suffix matches the curve:
	// ES256→P-256, ES384→P-384, ES512→P-521 (the "512" is historical,
	// the curve is P-521, not P-512).
	ES256 Algorithm = "ES256"
	ES384 Algorithm = "ES384"
	ES512 Algorithm = "ES512"
)

// Valid reports whether a is one of the supported algorithms.
func (a Algorithm) Valid() bool {
	switch a {
	case EdDSA,
		RS256, RS384, RS512,
		PS256, PS384, PS512,
		ES256, ES384, ES512:
		return true
	}
	return false
}

// DefaultAlgorithm is used when a caller does not specify an algorithm at
// key-generation time (e.g. the boot-time auto-generation).
const DefaultAlgorithm = EdDSA

// Status values for SigningKey. These are the literal strings persisted
// in the oidc_signing_keys.status column.
const (
	StatusActive   = "active"
	StatusRetiring = "retiring"
	StatusExpired  = "expired"
)

// Rotation window defaults. These are package-level constants rather
// than options to keep behaviour uniform across all callers; if they
// ever need to become configurable, they can be lifted into an Options
// struct without changing the public API.
const (
	// ActiveKeyMaxAge is how long a key can remain `active` before the
	// next Rotate mints its replacement. Rotation is driven by wall-clock
	// time rather than a ticker, so calls to Rotate within this window
	// are no-ops.
	ActiveKeyMaxAge = 7 * 24 * time.Hour

	// RetiringOverlap is how long a `retiring` key stays in the JWK Set
	// after its replacement is activated, so in-flight tokens still
	// verify.
	RetiringOverlap = 14 * 24 * time.Hour

	// ExpiredKeyPruneAge is how long an `expired` row stays in the DB
	// before DeleteExpiredBefore purges it. We keep expired rows around
	// long enough to answer "when did this kid exist" audit questions
	// well past the point any still-floating token has stopped being
	// trusted.
	ExpiredKeyPruneAge = 30 * 24 * time.Hour

	// DefaultTokenTTL is the default exp claim offset used by Signer.Mint.
	DefaultTokenTTL = 5 * time.Minute
)

// Sentinel errors.
var (
	ErrNoActiveKey    = errors.New("jwks: no active signing key")
	ErrUnsupportedAlg = errors.New("jwks: unsupported algorithm")
	ErrKeyNotFound    = errors.New("jwks: key not found")
	ErrKeyInvalid     = errors.New("jwks: key data invalid")
)

// JWK is the subset of JSON Web Key fields we emit from the handler.
// We don't expose go-jose's JSONWebKey directly because the marshaled
// shape depends on its private `rawJSONWebKey` struct; serializing the
// public key via json.RawMessage insulates callers from library drift.
type JWK = map[string]any
