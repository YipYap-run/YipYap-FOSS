package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SigningKey is the in-memory representation of a persisted key. The
// Private field holds the concrete Go crypto type for the Algorithm and
// must never be logged.
type SigningKey struct {
	KID         string
	Algorithm   Algorithm
	Private     any    // *rsa.PrivateKey | ed25519.PrivateKey | *ecdsa.PrivateKey
	PublicJWK   string // JSON of the public-component JWK (includes kid,kty,alg,use)
	Status      string // "active" | "retiring" | "expired"
	CreatedAt   time.Time
	ActivatedAt *time.Time
	RetiredAt   *time.Time
}

// KeyStore abstracts persistence for signing keys. The production
// implementation is dbKeyStore (backed by store.OIDCSigningKeyStore).
// Future KMS-backed implementations plug in here without touching
// Manager.
type KeyStore interface {
	Save(ctx context.Context, k *SigningKey) error
	Get(ctx context.Context, kid string) (*SigningKey, error)
	ListActive(ctx context.Context) ([]*SigningKey, error)
	// ListPublic returns keys the JWK Set should advertise: active and
	// retiring statuses. Expired keys are excluded.
	ListPublic(ctx context.Context) ([]*SigningKey, error)
	// ListAll returns every row regardless of status. Used by Rotate to
	// sweep retiring/expired rows.
	ListAll(ctx context.Context) ([]*SigningKey, error)
	UpdateStatus(ctx context.Context, kid, status string, retiredAt *time.Time) error
	// Prune deletes expired rows whose retired_at is before cutoff.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}

// dbKeyStore is the production KeyStore. It envelope-encrypts the PKCS8
// DER of each key on Save and decrypts on Get/List.
type dbKeyStore struct {
	store store.OIDCSigningKeyStore
	env   *crypto.Envelope
}

// NewDBKeyStore returns a KeyStore backed by s. The env pointer must be
// non-nil; an envelope with a passthrough key defeats the purpose of the
// envelope abstraction, which is why yipyap-web requires
// YIPYAP_JWKS_SIGNING_KEY be set before constructing this.
func NewDBKeyStore(s store.OIDCSigningKeyStore, env *crypto.Envelope) (KeyStore, error) {
	if s == nil {
		return nil, fmt.Errorf("jwks: nil store")
	}
	if env == nil {
		return nil, fmt.Errorf("jwks: envelope required for private-key encryption")
	}
	return &dbKeyStore{store: s, env: env}, nil
}

// Save serialises k.Private to PKCS#8 DER, encrypts it, and writes the
// row.
func (d *dbKeyStore) Save(ctx context.Context, k *SigningKey) error {
	der, err := marshalPKCS8(k.Private)
	if err != nil {
		return fmt.Errorf("jwks: marshal private: %w", err)
	}
	ct, err := d.env.Encrypt(der)
	if err != nil {
		// zero the plaintext buffer on the error path too.
		zero(der)
		return fmt.Errorf("jwks: encrypt private: %w", err)
	}
	zero(der)

	row := &store.OIDCSigningKey{
		Kid:              k.KID,
		Algorithm:        string(k.Algorithm),
		EncryptedPrivate: ct,
		PublicJWK:        k.PublicJWK,
		Status:           k.Status,
		CreatedAt:        k.CreatedAt,
		ActivatedAt:      k.ActivatedAt,
		RetiredAt:        k.RetiredAt,
	}
	return d.store.Create(ctx, row)
}

func (d *dbKeyStore) Get(ctx context.Context, kid string) (*SigningKey, error) {
	row, err := d.store.GetByKID(ctx, kid)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrKeyNotFound
	}
	return d.rowToKey(row)
}

func (d *dbKeyStore) ListActive(ctx context.Context) ([]*SigningKey, error) {
	rows, err := d.store.ListByStatus(ctx, StatusActive)
	if err != nil {
		return nil, err
	}
	return d.rowsToKeys(rows)
}

func (d *dbKeyStore) ListPublic(ctx context.Context) ([]*SigningKey, error) {
	rows, err := d.store.ListByStatus(ctx, StatusActive, StatusRetiring)
	if err != nil {
		return nil, err
	}
	return d.rowsToKeys(rows)
}

func (d *dbKeyStore) ListAll(ctx context.Context) ([]*SigningKey, error) {
	rows, err := d.store.ListByStatus(ctx)
	if err != nil {
		return nil, err
	}
	return d.rowsToKeys(rows)
}

func (d *dbKeyStore) UpdateStatus(ctx context.Context, kid, status string, retiredAt *time.Time) error {
	return d.store.UpdateStatus(ctx, kid, status, retiredAt)
}

func (d *dbKeyStore) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	return d.store.DeleteExpiredBefore(ctx, cutoff)
}

func (d *dbKeyStore) rowsToKeys(rows []*store.OIDCSigningKey) ([]*SigningKey, error) {
	out := make([]*SigningKey, 0, len(rows))
	for _, r := range rows {
		k, err := d.rowToKey(r)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func (d *dbKeyStore) rowToKey(r *store.OIDCSigningKey) (*SigningKey, error) {
	der, err := d.env.Decrypt(r.EncryptedPrivate)
	if err != nil {
		return nil, fmt.Errorf("jwks: decrypt private for kid %q: %w", r.Kid, err)
	}
	priv, err := parsePKCS8(der)
	zero(der)
	if err != nil {
		return nil, fmt.Errorf("jwks: parse private for kid %q: %w", r.Kid, err)
	}
	return &SigningKey{
		KID:         r.Kid,
		Algorithm:   Algorithm(r.Algorithm),
		Private:     priv,
		PublicJWK:   r.PublicJWK,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt,
		ActivatedAt: r.ActivatedAt,
		RetiredAt:   r.RetiredAt,
	}, nil
}

// marshalPKCS8 returns the PKCS#8 DER encoding of a supported private
// key. The stdlib's MarshalPKCS8PrivateKey handles all three of our
// supported types.
func marshalPKCS8(priv any) ([]byte, error) {
	switch priv.(type) {
	case ed25519.PrivateKey, *rsa.PrivateKey, *ecdsa.PrivateKey:
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedAlg, priv)
	}
	return x509.MarshalPKCS8PrivateKey(priv)
}

// parsePKCS8 parses a PKCS#8 private key and checks it is one of the
// three supported types.
func parsePKCS8(der []byte) (any, error) {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	switch k.(type) {
	case ed25519.PrivateKey, *rsa.PrivateKey, *ecdsa.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedAlg, k)
	}
}

// zero best-effort wipes b. Private-key bytes spend as little time as
// possible outside the envelope.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
