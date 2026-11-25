package handlers

import (
	"context"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type RateBudget interface {
	Take(ctx context.Context, key string, window time.Duration) (count int, resetAt time.Time, err error)
	Peek(ctx context.Context, key string, window time.Duration) (count int, resetAt time.Time, err error)
}

type rateBudget struct {
	store  store.RateBudgetStore
	memory *memoryBudget
}

func NewRateBudget(s store.Store) RateBudget {
	b := &rateBudget{memory: newMemoryBudget()}
	if s != nil {
		b.store = s.RateBudget()
	}
	return b
}

func (b *rateBudget) Take(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	now := time.Now().UTC()
	if b.store != nil {
		count, resetAt, err := b.store.Take(ctx, key, window, now)
		if err == nil {
			return count, resetAt, nil
		}
		log.Printf("rate budget: store take for %q: %v", key, err)
	}
	return b.memory.Take(key, window, now)
}

func (b *rateBudget) Peek(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	now := time.Now().UTC()
	if b.store != nil {
		count, resetAt, err := b.store.Peek(ctx, key, window, now)
		if err == nil {
			return count, resetAt, nil
		}
		log.Printf("rate budget: store peek for %q: %v", key, err)
	}
	return b.memory.Peek(key, window, now)
}

const (
	memoryBudgetMaxKeys = 10000
	memoryBudgetMaxAge  = 24 * time.Hour
)

type memoryBudget struct {
	mu      sync.Mutex
	entries map[string]budgetEntry
}

type budgetEntry struct {
	start time.Time
	count int
}

func newMemoryBudget() *memoryBudget {
	return &memoryBudget{entries: make(map[string]budgetEntry)}
}

func (m *memoryBudget) Take(key string, window time.Duration, now time.Time) (int, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.window(key, window, now)
	e.count++
	m.entries[key] = e
	m.prune(now)
	return e.count, e.start.Add(window), nil
}

func (m *memoryBudget) Peek(key string, window time.Duration, now time.Time) (int, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.window(key, window, now)
	return e.count, e.start.Add(window), nil
}

func (m *memoryBudget) window(key string, window time.Duration, now time.Time) budgetEntry {
	e, ok := m.entries[key]
	if !ok || !now.Before(e.start.Add(window)) {
		return budgetEntry{start: now}
	}
	return e
}

func (m *memoryBudget) prune(now time.Time) {
	if len(m.entries) <= memoryBudgetMaxKeys {
		return
	}
	cutoff := now.Add(-memoryBudgetMaxAge)
	for key, e := range m.entries {
		if e.start.Before(cutoff) {
			delete(m.entries, key)
		}
	}
}

func BudgetLimit(b RateBudget, scope string, limit int, window time.Duration, keyFn func(*http.Request) (string, error), onLimit http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := keyFn(r)
			if err != nil || key == "" {
				key = "unknown"
			}
			count, resetAt, err := b.Take(r.Context(), scope+":"+key, window)
			if err != nil {
				log.Printf("rate budget: limit %s: %v", scope, err)
				next.ServeHTTP(w, r)
				return
			}
			if count <= limit {
				next.ServeHTTP(w, r)
				return
			}
			wait := time.Until(resetAt)
			if wait <= 0 {
				wait = window
			}
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			if onLimit != nil {
				onLimit(w, r)
				return
			}
			jsonResponse(w, http.StatusTooManyRequests, map[string]any{
				"error":               "too many requests; retry after " + strconv.Itoa(secs) + " seconds",
				"code":                "rate_limited",
				"retry_after_seconds": secs,
			})
		})
	}
}
