package jwks

import "time"

// Observer is an optional instrumentation hook. All methods MUST be
// nil-safe at call sites because implementations are free to be nil. The
// intent is to let callers (yipyap-web) wire OTEL metrics without the
// jwks package depending on internal/telemetry.
type Observer interface {
	// OnSign is called after every successful JWT minting.
	OnSign(alg Algorithm, kid string)
	// OnRotate is called after every Rotate attempt. err is nil on
	// success. activeCount is the number of active+retiring keys after
	// the rotation completed; activeAge is the age of the current
	// active key (0 if none).
	OnRotate(err error, activeCount int, activeAge time.Duration)
}

// WithObserver attaches an Observer to the Manager.
func WithObserver(obs Observer) Option {
	return func(m *Manager) { m.obs = obs }
}
