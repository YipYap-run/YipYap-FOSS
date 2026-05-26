package oidc

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultVerifierCacheTTL is how long a Verifier is reused before being
// evicted and rebuilt. 10 minutes pairs with typical JWKS Cache-Control
// headers; rebuilding fetches fresh keys without forcing per-request
// discovery hits. See A4-N-6.
const DefaultVerifierCacheTTL = 10 * time.Minute

// VerifierCache memoizes *Verifier instances keyed by (issuer, jwks_url,
// audiences). Two callers with the same configuration share a single
// Verifier, which in turn shares a single underlying RemoteKeySet, so
// the upstream JWKS endpoint sees one warm-up fetch instead of one per
// caller.
//
// Entries are evicted lazily: Get() drops a TTL-expired entry on the
// way out and forces a rebuild on the next call. There is no background
// janitor, for the modest scale of OIDC verifiers we expect (single
// digit per process at steady state) this keeps the implementation
// trivially correct under contention.
//
// VerifierCache is safe for concurrent use.
type VerifierCache struct {
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	items map[string]*verifierCacheEntry
}

type verifierCacheEntry struct {
	verifier *Verifier
	expires  time.Time
}

// NewVerifierCache returns an empty cache with the given TTL. A
// non-positive TTL falls back to DefaultVerifierCacheTTL.
func NewVerifierCache(ttl time.Duration) *VerifierCache {
	if ttl <= 0 {
		ttl = DefaultVerifierCacheTTL
	}
	return &VerifierCache{
		ttl:   ttl,
		now:   time.Now,
		items: make(map[string]*verifierCacheEntry),
	}
}

// WithClock overrides the wall clock for tests.
func (c *VerifierCache) WithClock(now func() time.Time) *VerifierCache {
	c.now = now
	return c
}

// Get returns a Verifier for cfg, reusing a cached entry when present
// and unexpired. On a miss (or TTL eviction) build is invoked to
// construct a fresh Verifier; the result is stored under the same key
// before being returned.
//
// build runs OUTSIDE the cache mutex so a slow construction does not
// block sibling Get() calls for unrelated keys. The trade-off is that
// two cold misses on the same key can both run build concurrently, the
// loser's verifier is discarded. For verifiers this is acceptable
// because construction performs no network I/O (NewVerifier is cheap;
// JWKS fetch happens on first Verify and is itself singleflight-safe
// inside coreos/go-oidc).
func (c *VerifierCache) Get(cfg VerifierConfig, build func() *Verifier) *Verifier {
	key := verifierCacheKey(cfg)

	c.mu.Lock()
	if entry, ok := c.items[key]; ok {
		if c.now().Before(entry.expires) {
			c.mu.Unlock()
			return entry.verifier
		}
		// TTL-evicted; fall through to rebuild.
		delete(c.items, key)
	}
	c.mu.Unlock()

	v := build()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check: a concurrent caller may have populated the entry while
	// we were building. Prefer the existing entry to keep request flow
	// pinned to a single Verifier instance.
	if entry, ok := c.items[key]; ok && c.now().Before(entry.expires) {
		return entry.verifier
	}
	c.items[key] = &verifierCacheEntry{
		verifier: v,
		expires:  c.now().Add(c.ttl),
	}
	return v
}

// Invalidate removes the cached entry for cfg. Use this when a config
// rotation arrives, the next Get() will rebuild from scratch.
func (c *VerifierCache) Invalidate(cfg VerifierConfig) {
	key := verifierCacheKey(cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Size returns the current entry count. Exported for tests and
// operational diagnostics.
func (c *VerifierCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// verifierCacheKey produces a deterministic key from the parts that
// affect token verification semantics: Issuer, JWKSURL, and the sorted
// Audiences list. RefreshWindow is intentionally NOT in the key, it's
// a hint to the underlying library, not a verification rule.
func verifierCacheKey(cfg VerifierConfig) string {
	auds := append([]string(nil), cfg.Audiences...)
	sort.Strings(auds)
	var b strings.Builder
	b.Grow(len(cfg.Issuer) + len(cfg.JWKSURL) + 32)
	b.WriteString(cfg.Issuer)
	b.WriteByte('|')
	b.WriteString(cfg.JWKSURL)
	b.WriteByte('|')
	for i, a := range auds {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(a)
	}
	return b.String()
}
