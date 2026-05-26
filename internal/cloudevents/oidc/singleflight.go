package oidc

import "sync"

// singleflightGroup deduplicates concurrent calls keyed by a string. It
// is the in-repo equivalent of golang.org/x/sync/singleflight.Group with
// only the do() entry point we need. We carry our own copy because
// pulling singleflight in as a direct require would touch go.mod/go.sum,
// and the implementation below is small enough to vendor inline. See
// A4-N-5.
//
// Semantics: when N goroutines call do() concurrently with the same key,
// fn() runs exactly once. The first goroutine drives fn; the others
// block on the in-flight call's WaitGroup and receive the same
// (value, error) result. Once fn returns the entry is cleared so the
// next call re-runs fn.
//
// Failures are NOT cached, the caller (Client.Token) explicitly avoids
// caching errors. A second do() after a failed first do() will fire fn
// again, matching the behaviour of x/sync/singleflight under the same
// usage shape.
type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

// sfCall represents an in-flight or completed do() call. Multiple
// goroutines wait on wg; the first goroutine runs fn and then calls
// wg.Done() to release the rest.
type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// do executes fn exactly once for the given key when called
// concurrently. The shared result is returned to every caller. The
// boolean return reports whether THIS goroutine ran fn (true) or
// piggy-backed on another's call (false), useful for tests.
func (g *singleflightGroup) do(key string, fn func() (any, error)) (any, error, bool) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, false
	}
	c := &sfCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	return c.val, c.err, true
}
