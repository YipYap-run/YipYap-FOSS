// Package limits holds the operator-set resource caps for self-hosted
// installs. The self-hosted build has no plan limits: an operator may cap
// resources per org through environment variables, and nothing is capped
// unless they do.
package limits

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// Environment variables read by FromEnv. Unset or 0 means unlimited for the
// Max* caps.
const (
	EnvMaxMonitors             = "YIPYAP_MAX_MONITORS_PER_ORG"
	EnvMaxNotificationChannels = "YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG"
	EnvMaxEscalationPolicies   = "YIPYAP_MAX_ESCALATION_POLICIES"
	EnvMaxMembers              = "YIPYAP_MAX_MEMBERS"
	EnvMinCheckInterval        = "YIPYAP_MIN_CHECK_INTERVAL_SECONDS"
)

// DefaultMinCheckIntervalSeconds is the check interval floor when
// YIPYAP_MIN_CHECK_INTERVAL_SECONDS is unset. It matches the lowest floor any
// plan gets on the hosted service, so the scheduler is never asked for
// tighter loops than it is tuned for.
const DefaultMinCheckIntervalSeconds = 10

// maxCap bounds the accepted cap values so a typo like 1e12 fails startup
// instead of being taken as "effectively unlimited".
const maxCap = 1_000_000

// Caps are the per-org resource caps. A Max* value of 0 means unlimited.
type Caps struct {
	MaxMonitors             int `json:"max_monitors"`
	MaxNotificationChannels int `json:"max_notification_channels"`
	MaxEscalationPolicies   int `json:"max_escalation_policies"`
	MaxMembers              int `json:"max_members"`
	MinCheckIntervalSeconds int `json:"min_check_interval_seconds"`
}

// Default returns the caps of an install with no limit variables set:
// everything unlimited, default interval floor.
func Default() Caps {
	return Caps{MinCheckIntervalSeconds: DefaultMinCheckIntervalSeconds}
}

// FromEnv parses the caps from the environment. Any value that is not a
// whole number in range is an error naming the variable, so a bad value
// stops startup instead of being silently ignored.
func FromEnv(getenv func(string) string) (Caps, error) {
	c := Default()
	fields := []struct {
		env string
		dst *int
		min int
	}{
		{EnvMaxMonitors, &c.MaxMonitors, 0},
		{EnvMaxNotificationChannels, &c.MaxNotificationChannels, 0},
		{EnvMaxEscalationPolicies, &c.MaxEscalationPolicies, 0},
		{EnvMaxMembers, &c.MaxMembers, 0},
		{EnvMinCheckInterval, &c.MinCheckIntervalSeconds, 1},
	}
	for _, f := range fields {
		raw := strings.TrimSpace(getenv(f.env))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < f.min || n > maxCap {
			if f.min == 0 {
				return Caps{}, fmt.Errorf("%s must be a whole number from 0 (unlimited) to %d, got %q", f.env, maxCap, raw)
			}
			return Caps{}, fmt.Errorf("%s must be a whole number from %d to %d, got %q", f.env, f.min, maxCap, raw)
		}
		*f.dst = n
	}
	return c, nil
}

var current atomic.Pointer[Caps]

// Set installs the caps used by Current. Call it once at startup.
func Set(c Caps) {
	current.Store(&c)
}

// Current returns the installed caps, or Default when Set was never called.
func Current() Caps {
	if c := current.Load(); c != nil {
		return *c
	}
	return Default()
}

// CapError is returned when an operator-set cap blocks a create.
type CapError struct {
	Resource string // plural noun, e.g. "monitors"
	Max      int
	Env      string
	// Minimum switches the wording to an interval floor: Max is then the
	// shortest interval in seconds rather than a resource count.
	Minimum bool
}

func (e *CapError) Error() string {
	if e.Minimum {
		return fmt.Sprintf("minimum check interval on this server is %ds (set by %s; the server operator sets this limit). Ask your server administrator to lower it. Hosted YipYap runs without server caps to manage: https://yipyap.run/pricing", e.Max, e.Env)
	}
	return fmt.Sprintf("this install allows up to %d %s per organization. The server operator sets this limit with %s. Hosted YipYap runs without server caps to manage: https://yipyap.run/pricing", e.Max, e.Resource, e.Env)
}
