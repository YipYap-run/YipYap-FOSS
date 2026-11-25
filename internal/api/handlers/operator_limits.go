package handlers

import (
	"fmt"
	"sync"
)

// OperatorLimits are the optional caps a self-hosted operator sets with
// environment variables. FOSS has no plans, so with nothing set there is no
// monitor or channel cap, and the interval floor is a technical one.
type OperatorLimits struct {
	// MaxMonitorsPerOrg caps monitors per org. 0 means unlimited.
	MaxMonitorsPerOrg int
	// MaxNotificationChannelsPerOrg caps notification channels per org.
	// 0 means unlimited.
	MaxNotificationChannelsPerOrg int
	// MinCheckIntervalSeconds is the shortest check interval allowed.
	// 0 means DefaultMinCheckIntervalSeconds.
	MinCheckIntervalSeconds int
}

const (
	// DefaultMinCheckIntervalSeconds is the interval floor when the operator
	// sets none. Each monitor runs on its own ticker, so the floor keeps a
	// typo like 1 from turning into a flood of checks.
	DefaultMinCheckIntervalSeconds = 10
	// defaultCheckInterval is used when a create omits the interval.
	defaultCheckInterval = 60

	// Env var names, quoted in cap messages so the reader knows what to
	// change. internal/config reads the same names.
	envMaxMonitors     = "YIPYAP_MAX_MONITORS_PER_ORG"
	envMaxChannels     = "YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG"
	envMinInterval     = "YIPYAP_MIN_CHECK_INTERVAL_SECONDS"
	hostedPricingURL   = "https://yipyap.run/pricing"
	hostedProductBlurb = "Hosted YipYap runs without server caps to manage: " + hostedPricingURL
)

var (
	operatorLimitsMu sync.RWMutex
	operatorLimits   OperatorLimits
)

// SetOperatorLimits installs the operator caps. cmd/yipyap calls it once at
// startup. Negative values are treated as unset.
func SetOperatorLimits(l OperatorLimits) {
	if l.MaxMonitorsPerOrg < 0 {
		l.MaxMonitorsPerOrg = 0
	}
	if l.MaxNotificationChannelsPerOrg < 0 {
		l.MaxNotificationChannelsPerOrg = 0
	}
	if l.MinCheckIntervalSeconds < 0 {
		l.MinCheckIntervalSeconds = 0
	}
	operatorLimitsMu.Lock()
	operatorLimits = l
	operatorLimitsMu.Unlock()
}

// CurrentOperatorLimits returns the caps in effect, with the interval floor
// resolved to its default when unset.
func CurrentOperatorLimits() OperatorLimits {
	operatorLimitsMu.RLock()
	l := operatorLimits
	operatorLimitsMu.RUnlock()
	if l.MinCheckIntervalSeconds == 0 {
		l.MinCheckIntervalSeconds = DefaultMinCheckIntervalSeconds
	}
	return l
}

// newOperatorCapError builds a 403 for an operator cap. It names the
// setting so an admin knows what to raise, and points at the hosted
// product. It never asks for an upgrade, because FOSS has none.
func newOperatorCapError(what string, max int, envVar string) error {
	return &planError{
		msg: fmt.Sprintf("this server allows up to %d %s per organization (set by %s). "+
			"Ask your server administrator to raise it. %s", max, what, envVar, hostedProductBlurb),
		operatorCap: true,
	}
}
