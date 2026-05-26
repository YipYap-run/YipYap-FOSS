package store

import (
	"context"
	"time"
)

// OIDCSigningKey is a persisted JWT-signing key used by the self-hosted
// JWKS endpoint. The private key is envelope-encrypted (AES-GCM) before
// persistence; callers are responsible for en/decrypting around this
// interface.
type OIDCSigningKey struct {
	Kid              string
	Algorithm        string // "EdDSA" | "RS256" | "ES256"
	EncryptedPrivate []byte
	PublicJWK        string // JSON-encoded JWK of the public component
	Status           string // "active" | "retiring" | "expired"
	CreatedAt        time.Time
	ActivatedAt      *time.Time
	RetiredAt        *time.Time
}

// OIDCSigningKeyStore persists OIDCSigningKey rows.
type OIDCSigningKeyStore interface {
	Create(ctx context.Context, k *OIDCSigningKey) error
	GetByKID(ctx context.Context, kid string) (*OIDCSigningKey, error)
	// ListByStatus returns rows whose status matches any of the given
	// values. Passing no statuses returns all rows.
	ListByStatus(ctx context.Context, statuses ...string) ([]*OIDCSigningKey, error)
	UpdateStatus(ctx context.Context, kid, status string, retiredAt *time.Time) error
	// DeleteExpiredBefore removes rows whose status='expired' and whose
	// retired_at is strictly before cutoff. Returns the number of rows
	// deleted.
	DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
