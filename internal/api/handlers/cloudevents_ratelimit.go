package handlers

import (
	"net/http"
	"sync"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
)

// DefaultMaxStreamsPerOrg caps the number of concurrent /cloudevents/stream
// connections per org. A stream connection pins a server goroutine plus
// a 1024-slot buffered channel; without the cap an authenticated caller
// could open thousands and exhaust memory. See H-16.
const DefaultMaxStreamsPerOrg = 10

// RateLimitKeyByOrg is an httprate.KeyFunc that keys on the
// authenticated org ID. Requires auth middleware to have populated
// claims on the request context (i.e. use this AFTER authMiddleware).
// Requests without claims fall back to "anonymous", those are 401'd
// before reaching the handler but keying defensively avoids a nil
// panic inside httprate.
func RateLimitKeyByOrg(r *http.Request) (string, error) {
	if claims := middleware.GetClaims(r.Context()); claims != nil && claims.OrgID != "" {
		return "org:" + claims.OrgID, nil
	}
	return "anonymous", nil
}

// StreamConcurrencyGate caps concurrent long-lived /cloudevents/stream
// connections per org. Middleware increments on entry and decrements on
// exit; past the cap it returns 429.
type StreamConcurrencyGate struct {
	max int
	mu  sync.Mutex
	n   map[string]int
}

// NewStreamConcurrencyGate returns a gate with the given per-org cap.
// A non-positive max falls back to DefaultMaxStreamsPerOrg.
func NewStreamConcurrencyGate(max int) *StreamConcurrencyGate {
	if max <= 0 {
		max = DefaultMaxStreamsPerOrg
	}
	return &StreamConcurrencyGate{
		max: max,
		n:   make(map[string]int),
	}
}

// Middleware gates the wrapped handler by per-org concurrent-stream
// count. Requests beyond the cap receive 429 with a Retry-After hint.
func (g *StreamConcurrencyGate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r.Context())
		if claims == nil || claims.OrgID == "" {
			// Let auth middleware have handled this; defense-in-depth
			// path here returns 401 rather than booking a gate slot for
			// an anonymous request.
			errorResponse(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		orgID := claims.OrgID
		if !g.enter(orgID) {
			w.Header().Set("Retry-After", "30")
			errorResponse(w, http.StatusTooManyRequests, "stream concurrency cap exceeded for org")
			return
		}
		defer g.leave(orgID)
		next.ServeHTTP(w, r)
	})
}

func (g *StreamConcurrencyGate) enter(orgID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n[orgID] >= g.max {
		return false
	}
	g.n[orgID]++
	return true
}

func (g *StreamConcurrencyGate) leave(orgID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n[orgID] > 0 {
		g.n[orgID]--
	}
	if g.n[orgID] == 0 {
		delete(g.n, orgID)
	}
}

// active is a test helper returning the current concurrent-stream count
// for orgID.
func (g *StreamConcurrencyGate) active(orgID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n[orgID]
}
