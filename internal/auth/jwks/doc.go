// Package jwks implements the self-hosted OIDC identity-provider surface
// yipyap SaaS uses to sign CloudEvent reply tokens. It owns:
//
//   - signing-key generation and envelope-encrypted persistence
//   - weekly rotation with a two-week overlap window
//   - a JSON Web Key Set (JWKS) HTTP handler served at
//     /.well-known/jwks.json
//   - a kid-aware JWT Signer for callers that need to mint tokens
//
// The package supports EdDSA (Ed25519), RS256 (RSA-2048), and ES256
// (NIST P-256). EdDSA is the default for newly-generated keys.
//
// Private keys never leave the process in plaintext outside this package:
// they are stored in the database via crypto.Envelope and decrypted only
// when needed to sign or rotate.
//
// This package is build-tag gated out of FOSS builds via the consumer
// wiring in internal/api (see jwks_routes_foss.go / jwks_routes_full.go).
// The package itself compiles under both tags so that non-wired use (e.g.
// tests, internal tooling) remains possible, but the FOSS yipyap-web
// binary does not mount the handler or run the rotation goroutine.
package jwks
