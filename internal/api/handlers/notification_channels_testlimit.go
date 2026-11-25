package handlers

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Channel test rate limits, per org. Every test sends a real message, and
// an SMS or voice test spends money, so phone types get a tighter cap on
// top of the overall one.
const (
	channelTestLimit       = 10
	channelTestWindow      = time.Minute
	phoneChannelTestLimit  = 3
	phoneChannelTestWindow = 10 * time.Minute
)

// channelTestLimiter is a sliding-window counter keyed by org. It is
// in-process like the other API rate limits, so each replica enforces the
// caps on its own share of the traffic; the phone quota still applies
// across replicas.
type channelTestLimiter struct {
	mu   sync.Mutex
	now  func() time.Time
	hits map[string][]time.Time
}

func newChannelTestLimiter() *channelTestLimiter {
	return &channelTestLimiter{now: time.Now, hits: make(map[string][]time.Time)}
}

// Limit scopes reported to the client in a 429, so the UI can hold back
// the Test buttons the limit covers: every channel, or phone channels.
const (
	testLimitScopeOrg   = "org"
	testLimitScopePhone = "phone"
)

type testRateRule struct {
	key    string
	scope  string
	limit  int
	window time.Duration
}

// allow records one test for orgID when every rule that applies has room,
// and otherwise returns how long until the tightest one frees a slot and
// the scope of that rule.
func (l *channelTestLimiter) allow(orgID, channelType string) (bool, time.Duration, string) {
	rules := []testRateRule{{"org:" + orgID, testLimitScopeOrg, channelTestLimit, channelTestWindow}}
	if isPhoneChannel(channelType) {
		rules = append(rules, testRateRule{"phone:" + orgID, testLimitScopePhone, phoneChannelTestLimit, phoneChannelTestWindow})
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	var scope string
	for _, rule := range rules {
		kept := prune(l.hits[rule.key], now.Add(-rule.window))
		l.hits[rule.key] = kept
		if len(kept) >= rule.limit {
			// The oldest hit in the window leaves it first.
			if d := kept[0].Add(rule.window).Sub(now); d > wait {
				wait, scope = d, rule.scope
			}
		}
	}
	if wait > 0 {
		return false, wait, scope
	}
	for _, rule := range rules {
		l.hits[rule.key] = append(l.hits[rule.key], now)
	}
	if len(l.hits) > 4096 {
		l.sweep(now)
	}
	return true, 0, ""
}

// sweep drops keys with no hits inside the longest window, so orgs that
// tested once do not stay in the map.
func (l *channelTestLimiter) sweep(now time.Time) {
	cutoff := now.Add(-phoneChannelTestWindow)
	for k, v := range l.hits {
		if len(prune(v, cutoff)) == 0 {
			delete(l.hits, k)
		}
	}
}

func prune(hits []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(hits) && !hits[i].After(cutoff) {
		i++
	}
	return hits[i:]
}

// writeTestRateLimited sends 429 with a Retry-After in whole seconds. The
// body repeats the wait as retry_after and names the limit_scope ("org" or
// "phone"), since the SPA's API client does not expose response headers.
func writeTestRateLimited(w http.ResponseWriter, wait time.Duration, scope string) {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	jsonResponse(w, http.StatusTooManyRequests, map[string]any{
		"error":       "too many test notifications, try again in " + strconv.Itoa(secs) + " seconds",
		"retry_after": secs,
		"limit_scope": scope,
	})
}
