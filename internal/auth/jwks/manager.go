package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
)

// Option configures a Manager.
type Option func(*Manager)

// WithClock overrides the clock used for rotation and status decisions.
// Test-only.
func WithClock(clock func() time.Time) Option {
	return func(m *Manager) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// WithRandReader overrides the entropy source used for keygen. Test-only.
func WithRandReader(r io.Reader) Option {
	return func(m *Manager) {
		if r != nil {
			m.rand = r
		}
	}
}

// Manager owns generation, rotation, and lookup of signing keys. It is
// safe for concurrent use; the only shared state is serialised through
// the KeyStore plus a mutex that protects Rotate from itself.
type Manager struct {
	store KeyStore
	clock func() time.Time
	rand  io.Reader
	obs   Observer

	rotateMu sync.Mutex
}

// NewManager constructs a Manager. opts override the defaults (time.Now,
// crypto/rand).
func NewManager(ks KeyStore, opts ...Option) *Manager {
	m := &Manager{
		store: ks,
		clock: time.Now,
		rand:  rand.Reader,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Generate produces a fresh signing key in memory. The caller decides
// whether to activate it; this function does not touch the KeyStore.
func (m *Manager) Generate(alg Algorithm) (*SigningKey, error) {
	if !alg.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	kid := newKID()
	var (
		priv   any
		pubJWK []byte
		err    error
	)
	switch alg {
	case EdDSA:
		_, sk, gerr := ed25519.GenerateKey(m.rand)
		if gerr != nil {
			return nil, fmt.Errorf("jwks: generate ed25519: %w", gerr)
		}
		priv = sk
		pubJWK, err = publicJWK(sk.Public(), kid, string(alg))
	case RS256, RS384, RS512, PS256, PS384, PS512:
		// All RSA variants (PKCS#1 v1.5 and PSS) share the same 2048-bit
		// key material; only the hash and padding differ at sign time.
		sk, gerr := rsa.GenerateKey(m.rand, 2048)
		if gerr != nil {
			return nil, fmt.Errorf("jwks: generate rsa: %w", gerr)
		}
		priv = sk
		pubJWK, err = publicJWK(&sk.PublicKey, kid, string(alg))
	case ES256:
		sk, gerr := ecdsa.GenerateKey(elliptic.P256(), m.rand)
		if gerr != nil {
			return nil, fmt.Errorf("jwks: generate ecdsa p-256: %w", gerr)
		}
		priv = sk
		pubJWK, err = publicJWK(&sk.PublicKey, kid, string(alg))
	case ES384:
		sk, gerr := ecdsa.GenerateKey(elliptic.P384(), m.rand)
		if gerr != nil {
			return nil, fmt.Errorf("jwks: generate ecdsa p-384: %w", gerr)
		}
		priv = sk
		pubJWK, err = publicJWK(&sk.PublicKey, kid, string(alg))
	case ES512:
		// ES512 uses curve P-521 per RFC 7518 §3.4, the "512" refers to
		// the SHA-512 digest, not the curve size.
		sk, gerr := ecdsa.GenerateKey(elliptic.P521(), m.rand)
		if gerr != nil {
			return nil, fmt.Errorf("jwks: generate ecdsa p-521: %w", gerr)
		}
		priv = sk
		pubJWK, err = publicJWK(&sk.PublicKey, kid, string(alg))
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	if err != nil {
		return nil, fmt.Errorf("jwks: encode public jwk: %w", err)
	}
	return &SigningKey{
		KID:       kid,
		Algorithm: alg,
		Private:   priv,
		PublicJWK: string(pubJWK),
		Status:    StatusActive, // caller may demote before Save if needed
		CreatedAt: m.clock().UTC(),
	}, nil
}

// Rotate performs the weekly rotation cycle:
//  1. if the current active key was created within ActiveKeyMaxAge,
//     Rotate is a no-op;
//  2. otherwise mint a new key of DefaultAlgorithm, mark the old active
//     as retiring with retired_at=now, and activate the new key;
//  3. demote retiring keys whose retired_at is older than RetiringOverlap
//     to expired;
//  4. hard-delete expired keys whose retired_at is older than
//     ExpiredKeyPruneAge.
//
// The function is safe to call repeatedly; subsequent calls within the
// same active-key window are no-ops.
func (m *Manager) Rotate(ctx context.Context) error {
	m.rotateMu.Lock()
	defer m.rotateMu.Unlock()

	now := m.clock().UTC()

	err := m.rotateLocked(ctx, now)
	if m.obs != nil {
		cnt, age := m.snapshot(ctx, now)
		m.obs.OnRotate(err, cnt, age)
	}
	return err
}

func (m *Manager) snapshot(ctx context.Context, now time.Time) (int, time.Duration) {
	pub, _ := m.store.ListPublic(ctx)
	var age time.Duration
	for _, k := range pub {
		if k.Status == StatusActive && k.CreatedAt.Before(now) {
			age = now.Sub(k.CreatedAt)
		}
	}
	return len(pub), age
}

func (m *Manager) rotateLocked(ctx context.Context, now time.Time) error {
	all, err := m.store.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("jwks: list all keys: %w", err)
	}

	// Step 3 + 4 apply regardless of whether we mint a new key.
	defer func() {
		// best-effort demote/prune; errors reported through the main err path.
	}()

	active := latestActive(all)

	// Step 1: decide whether to mint.
	mint := active == nil || now.Sub(active.CreatedAt) >= ActiveKeyMaxAge

	if mint {
		newKey, err := m.Generate(DefaultAlgorithm)
		if err != nil {
			return err
		}
		activatedAt := now
		newKey.Status = StatusActive
		newKey.CreatedAt = now
		newKey.ActivatedAt = &activatedAt

		// Persist first so we never end up with two actives if Save races.
		// On success, retire the previous active.
		if err := m.store.Save(ctx, newKey); err != nil {
			return fmt.Errorf("jwks: save new active: %w", err)
		}
		if active != nil {
			retired := now
			if err := m.store.UpdateStatus(ctx, active.KID, StatusRetiring, &retired); err != nil {
				return fmt.Errorf("jwks: retire previous active %s: %w", active.KID, err)
			}
		}
	}

	// Step 3: demote over-overlap retiring keys to expired.
	all, err = m.store.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("jwks: re-list keys: %w", err)
	}
	for _, k := range all {
		if k.Status != StatusRetiring {
			continue
		}
		if k.RetiredAt == nil {
			continue
		}
		if now.Sub(*k.RetiredAt) >= RetiringOverlap {
			if err := m.store.UpdateStatus(ctx, k.KID, StatusExpired, k.RetiredAt); err != nil {
				return fmt.Errorf("jwks: expire retiring key %s: %w", k.KID, err)
			}
		}
	}

	// Step 4: prune expired keys older than ExpiredKeyPruneAge.
	cutoff := now.Add(-ExpiredKeyPruneAge)
	if _, err := m.store.Prune(ctx, cutoff); err != nil {
		return fmt.Errorf("jwks: prune expired: %w", err)
	}
	return nil
}

// ListPublicJWKs returns the JWK Set representing keys an external
// verifier should trust right now (active + retiring).
func (m *Manager) ListPublicJWKs(ctx context.Context) ([]JWK, error) {
	keys, err := m.store.ListPublic(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]JWK, 0, len(keys))
	for _, k := range keys {
		var jwk JWK
		if err := json.Unmarshal([]byte(k.PublicJWK), &jwk); err != nil {
			return nil, fmt.Errorf("jwks: decode stored JWK for kid %s: %w", k.KID, err)
		}
		out = append(out, jwk)
	}
	return out, nil
}

// GetActiveSigner returns a Signer bound to the current active key. If
// there are multiple active rows (shouldn't happen, but defensively), the
// most recently created one wins.
func (m *Manager) GetActiveSigner(ctx context.Context) (*Signer, error) {
	actives, err := m.store.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	if len(actives) == 0 {
		return nil, ErrNoActiveKey
	}
	sort.Slice(actives, func(i, j int) bool {
		return actives[i].CreatedAt.After(actives[j].CreatedAt)
	})
	s, err := newSigner(actives[0])
	if err != nil {
		return nil, err
	}
	s.obs = m.obs
	return s, nil
}

// SaveAndActivate persists k with Status=active and activated_at=now.
// Called by EnsureInitialKey for first boot.
func (m *Manager) SaveAndActivate(ctx context.Context, k *SigningKey) error {
	now := m.clock().UTC()
	k.Status = StatusActive
	k.ActivatedAt = &now
	if k.CreatedAt.IsZero() {
		k.CreatedAt = now
	}
	return m.store.Save(ctx, k)
}

// latestActive picks the most-recently-created active key from all.
// Returns nil if none are active.
func latestActive(all []*SigningKey) *SigningKey {
	var out *SigningKey
	for _, k := range all {
		if k.Status != StatusActive {
			continue
		}
		if out == nil || k.CreatedAt.After(out.CreatedAt) {
			out = k
		}
	}
	return out
}

// publicJWK marshals pub (the public half of the key) into a JWK JSON
// object, injecting kid/alg/use.
func publicJWK(pub any, kid, alg string) ([]byte, error) {
	jwk := jose.JSONWebKey{
		Key:       pub,
		KeyID:     kid,
		Algorithm: alg,
		Use:       "sig",
	}
	return jwk.MarshalJSON()
}

// newKID generates a new key identifier. Using a UUID keeps the values
// short and opaque; they are included unchanged in JWT headers and in
// the JWK Set.
func newKID() string {
	return uuid.New().String()
}
