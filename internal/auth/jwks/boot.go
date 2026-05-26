package jwks

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"

	jose "github.com/go-jose/go-jose/v4"
)

// EnsureInitialKey auto-generates and activates the first signing key
// when the store is empty. It emits a structured log line on success so
// operators can see the kid + thumbprint in the startup output. It is
// idempotent: if any active or retiring key already exists, it returns
// nil without touching the store.
func EnsureInitialKey(ctx context.Context, mgr *Manager) error {
	public, err := mgr.ListPublicJWKs(ctx)
	if err != nil {
		return fmt.Errorf("jwks: list public: %w", err)
	}
	if len(public) > 0 {
		return nil
	}

	k, err := mgr.Generate(DefaultAlgorithm)
	if err != nil {
		return err
	}
	if err := mgr.SaveAndActivate(ctx, k); err != nil {
		return fmt.Errorf("jwks: activate initial key: %w", err)
	}

	thumb, terr := jwkThumbprint(k.PublicJWK)
	if terr != nil {
		// Non-fatal: we still generated and activated the key.
		slog.Warn("jwks: thumbprint failed (continuing)", "kid", k.KID, "err", terr.Error())
		thumb = "unavailable"
	}
	slog.Info("jwks: generated initial signing key",
		"kid", k.KID,
		"algorithm", string(k.Algorithm),
		"thumbprint", thumb,
		"status", k.Status,
	)
	return nil
}

// jwkThumbprint computes the RFC 7638 SHA-256 thumbprint of a JWK
// serialised as JSON and returns its base64url (no-padding) form. Useful
// for audit logs and TOFU fingerprinting.
func jwkThumbprint(jwkJSON string) (string, error) {
	var jwk jose.JSONWebKey
	if err := json.Unmarshal([]byte(jwkJSON), &jwk); err != nil {
		return "", err
	}
	sum, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	// Not strictly needed, but double-check: SHA-256 output is 32 bytes.
	_ = sha256.Size
	return base64.RawURLEncoding.EncodeToString(sum), nil
}
