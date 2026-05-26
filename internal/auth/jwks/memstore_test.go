package jwks

import (
	"context"
	"sort"
	"sync"
	"time"
)

// memKeyStore is an in-memory KeyStore used across this package's tests.
// It intentionally skips envelope encryption, tests exercise Manager,
// Signer, and Handler semantics, not encryption round-tripping. The
// dbKeyStore integration path has its own tests (see the store layer).
type memKeyStore struct {
	mu   sync.Mutex
	rows map[string]*SigningKey
}

func newMemStore() *memKeyStore {
	return &memKeyStore{rows: map[string]*SigningKey{}}
}

func (m *memKeyStore) Save(_ context.Context, k *SigningKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// deep-ish copy for the stored row so tests can mutate their local
	// references without affecting the store.
	cp := *k
	m.rows[k.KID] = &cp
	return nil
}

func (m *memKeyStore) Get(_ context.Context, kid string) (*SigningKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.rows[kid]
	if !ok {
		return nil, ErrKeyNotFound
	}
	cp := *k
	return &cp, nil
}

func (m *memKeyStore) ListActive(ctx context.Context) ([]*SigningKey, error) {
	return m.listByStatus(StatusActive), nil
}

func (m *memKeyStore) ListPublic(ctx context.Context) ([]*SigningKey, error) {
	return m.listByStatus(StatusActive, StatusRetiring), nil
}

func (m *memKeyStore) ListAll(ctx context.Context) ([]*SigningKey, error) {
	return m.listByStatus(), nil
}

func (m *memKeyStore) listByStatus(statuses ...string) []*SigningKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*SigningKey
	for _, k := range m.rows {
		if len(statuses) == 0 {
			cp := *k
			out = append(out, &cp)
			continue
		}
		for _, s := range statuses {
			if k.Status == s {
				cp := *k
				out = append(out, &cp)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *memKeyStore) UpdateStatus(_ context.Context, kid, status string, retiredAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.rows[kid]
	if !ok {
		return ErrKeyNotFound
	}
	k.Status = status
	k.RetiredAt = retiredAt
	return nil
}

func (m *memKeyStore) Prune(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var deleted int64
	for kid, k := range m.rows {
		if k.Status == StatusExpired && k.RetiredAt != nil && k.RetiredAt.Before(cutoff) {
			delete(m.rows, kid)
			deleted++
		}
	}
	return deleted, nil
}
