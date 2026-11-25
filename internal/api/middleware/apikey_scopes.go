package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

// API key scope vocabulary. This file is the single source of truth: the
// create handler validates against it, GET /org/api-keys/scopes serves it to
// the UI, and APIKeyScopeGuard enforces it on every authenticated route.
//
// A scope is "<resource>:read" or "<resource>:write" (write implies read), or
// one of the presets "read-only" (read on every resource) and "full-access"
// (read and write on every resource). Keys stored before this vocabulary
// existed may hold a bare resource name ("monitors", which means read and
// write on it), "read" (same as read-only) or no scopes at all (same as
// full-access). Those legacy forms are honored but cannot be created.

const (
	ScopePresetReadOnly   = "read-only"
	ScopePresetFullAccess = "full-access"

	ScopeActionRead  = "read"
	ScopeActionWrite = "write"
)

// ScopePreset is a named bundle of scopes shown as a role in the UI.
type ScopePreset struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// APIKeyScopeResource is one resource an API key can be granted access to.
// Actions lists only what the routes actually allow an API key to do:
// incidents and status pages are changed by people, so they offer read only.
type APIKeyScopeResource struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
}

// Scopes returns the concrete scope strings for the resource, for example
// ["monitors:read", "monitors:write"].
func (r APIKeyScopeResource) Scopes() []string {
	out := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		out = append(out, r.Key+":"+a)
	}
	return out
}

var (
	readWrite = []string{ScopeActionRead, ScopeActionWrite}
	readOnly  = []string{ScopeActionRead}
)

var apiKeyScopePresets = []ScopePreset{
	{Key: ScopePresetReadOnly, Label: "Read-only", Description: "Read every resource listed below. Cannot create, change or delete anything."},
	{Key: ScopePresetFullAccess, Label: "Full access", Description: "Read every resource listed below and change the ones that allow API keys to write. Covers resources added later."},
}

var apiKeyScopeResources = []APIKeyScopeResource{
	{Key: "monitors", Label: "Monitors", Description: "Monitors, monitor groups, monitor states, check history, uptime and latency.", Actions: readWrite},
	{Key: "alerts", Label: "Alerts", Description: "Alerts and their timelines. Write allows acknowledge and resolve.", Actions: readWrite},
	{Key: "incidents", Label: "Incidents", Description: "Incidents and incident updates. Creating or changing them needs a signed-in member.", Actions: readOnly},
	{Key: "escalation_policies", Label: "Escalation policies", Description: "Escalation policies and their steps.", Actions: readWrite},
	{Key: "notification_channels", Label: "Notification channels", Description: "Notification channels. Write also allows sending a test notification.", Actions: readWrite},
	{Key: "maintenance_windows", Label: "Maintenance windows", Description: "Scheduled maintenance windows.", Actions: readWrite},
	{Key: "teams", Label: "Teams", Description: "Teams and team membership.", Actions: readWrite},
	{Key: "schedules", Label: "On-call schedules", Description: "On-call schedules, overrides and who is on call.", Actions: readWrite},
	{Key: "status_pages", Label: "Status pages", Description: "Status pages and their layout. Changing them needs a signed-in member.", Actions: readOnly},
	{Key: "services", Label: "Services", Description: "Services, links and dependencies.", Actions: readWrite},
	{Key: "users", Label: "Members", Description: "List org members and their roles. Members can only be changed by an owner or admin session.", Actions: readOnly},
	{Key: "org", Label: "Organization", Description: "Org name, plan and settings. Org settings can only be changed by an owner or admin session.", Actions: readOnly},
	{Key: "events", Label: "Event stream", Description: "Poll or stream org events (CloudEvents transport and the Knative source).", Actions: readOnly},
}

// APIKeyScopePresets returns the preset scopes, in display order.
func APIKeyScopePresets() []ScopePreset {
	out := make([]ScopePreset, len(apiKeyScopePresets))
	copy(out, apiKeyScopePresets)
	return out
}

// APIKeyScopeResources returns the per-resource scopes, in display order.
func APIKeyScopeResources() []APIKeyScopeResource {
	out := make([]APIKeyScopeResource, len(apiKeyScopeResources))
	copy(out, apiKeyScopeResources)
	return out
}

func lookupScopeResource(key string) (APIKeyScopeResource, bool) {
	for _, r := range apiKeyScopeResources {
		if r.Key == key {
			return r, true
		}
	}
	return APIKeyScopeResource{}, false
}

func (r APIKeyScopeResource) supports(action string) bool {
	for _, a := range r.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// NormalizeAPIKeyScopes validates scopes requested for a new key and returns
// them in canonical form: trimmed, deduplicated, presets first and resources
// in vocabulary order. A scope already covered by another one (monitors:read
// next to monitors:write, any :read next to read-only, anything next to
// full-access) is dropped. An empty list or an unknown scope is an error.
func NormalizeAPIKeyScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("choose at least one scope")
	}
	presets := map[string]bool{}
	grants := map[string]string{} // resource -> strongest action
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		switch s {
		case ScopePresetReadOnly, ScopePresetFullAccess:
			presets[s] = true
			continue
		}
		res, action, ok := strings.Cut(s, ":")
		if !ok {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		r, found := lookupScopeResource(res)
		if !found || !r.supports(action) {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		if grants[res] != ScopeActionWrite {
			grants[res] = action
		}
	}
	if presets[ScopePresetFullAccess] {
		return []string{ScopePresetFullAccess}, nil
	}
	var out []string
	if presets[ScopePresetReadOnly] {
		out = append(out, ScopePresetReadOnly)
	}
	for _, r := range apiKeyScopeResources {
		action, ok := grants[r.Key]
		if !ok {
			continue
		}
		if action == ScopeActionRead && presets[ScopePresetReadOnly] {
			continue
		}
		out = append(out, r.Key+":"+action)
	}
	return out, nil
}

// scopeGrants reports whether scopes allow action on resource. An empty
// list is a legacy unrestricted key and is treated as full-access.
func scopeGrants(scopes []string, resource, action string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		switch s {
		case ScopePresetFullAccess:
			return true
		case ScopePresetReadOnly, "read":
			if action == ScopeActionRead {
				return true
			}
			continue
		case resource: // legacy bare resource name: read and write
			return true
		case resource + ":" + ScopeActionWrite:
			return true
		case resource + ":" + ScopeActionRead:
			if action == ScopeActionRead {
				return true
			}
		}
	}
	return false
}

func actionForMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ScopeActionRead
	}
	return ScopeActionWrite
}

// scopeRoute maps a path pattern (relative to /api/v1) to the resource an
// API key needs. A trailing "/**" matches the path itself and anything below
// it; "*" matches one segment. The first matching rule wins.
type scopeRoute struct {
	pattern  string
	resource string
	// alsoAccept lists resources whose read scope also grants this route.
	alsoAccept []string
	// readOnly routes write only a cursor or another read position, so a
	// read scope is enough even though the method is a write.
	readOnly bool
}

var apiKeyScopeRoutes = []scopeRoute{
	{pattern: "/alerts/*/create-incident", resource: "incidents"},
	{pattern: "/monitors/**", resource: "monitors"},
	{pattern: "/monitor-groups/**", resource: "monitors"},
	{pattern: "/monitor-states/**", resource: "monitors"},
	{pattern: "/alerts/**", resource: "alerts"},
	{pattern: "/incidents/**", resource: "incidents"},
	{pattern: "/escalation-policies/**", resource: "escalation_policies"},
	{pattern: "/notification-channels/**", resource: "notification_channels"},
	{pattern: "/maintenance-windows/**", resource: "maintenance_windows"},
	{pattern: "/teams/**", resource: "teams"},
	{pattern: "/schedules/**", resource: "schedules"},
	{pattern: "/status-pages/**", resource: "status_pages"},
	{pattern: "/services/**", resource: "services"},
	{pattern: "/users/**", resource: "users"},
	{pattern: "/org", resource: "org"},
	{pattern: "/org/settings", resource: "org"},
	// The event stream carries monitor and alert changes, so a key that can
	// read either keeps working with the Knative source it was issued for.
	{pattern: "/cloudevents/poll", resource: "events", alsoAccept: []string{"monitors", "alerts"}},
	{pattern: "/cloudevents/stream", resource: "events", alsoAccept: []string{"monitors", "alerts"}},
	{pattern: "/cloudevents/ack", resource: "events", alsoAccept: []string{"monitors", "alerts"}, readOnly: true},
	{pattern: "/knative/**", resource: "events", alsoAccept: []string{"monitors", "alerts"}},
}

func matchScopePattern(pattern, path string) bool {
	prefix, deep := strings.CutSuffix(pattern, "/**")
	if deep {
		pattern = prefix
	}
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	sp := strings.Split(strings.Trim(path, "/"), "/")
	if len(sp) < len(pp) || (!deep && len(sp) != len(pp)) {
		return false
	}
	for i, seg := range pp {
		if seg != "*" && seg != sp[i] {
			return false
		}
	}
	return true
}

// apiKeyRouteAllowed decides whether an API key with scopes may call method
// on path. It returns the scope the request needed for the error message;
// required is empty when the route is not open to API keys at all.
func apiKeyRouteAllowed(scopes []string, method, path string) (ok bool, required string) {
	path = strings.TrimPrefix(path, "/api/v1")
	for _, rt := range apiKeyScopeRoutes {
		if !matchScopePattern(rt.pattern, path) {
			continue
		}
		action := actionForMethod(method)
		if rt.readOnly {
			action = ScopeActionRead
		}
		res, _ := lookupScopeResource(rt.resource)
		if !res.supports(action) {
			return false, ""
		}
		if scopeGrants(scopes, rt.resource, action) {
			return true, ""
		}
		if action == ScopeActionRead {
			for _, alt := range rt.alsoAccept {
				if scopeGrants(scopes, alt, ScopeActionRead) {
					return true, ""
				}
			}
		}
		return false, rt.resource + ":" + action
	}
	return false, ""
}

// APIKeyScopeGuard limits API key requests to the routes their scopes cover.
// Every authenticated route group runs it (see authenticatedChain), so a
// route added without its own RequireScope is still closed to API keys until
// it is mapped in apiKeyScopeRoutes. Session (human) requests pass through.
func APIKeyScopeGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil || claims.Role != "api_key" {
				next.ServeHTTP(w, r)
				return
			}
			ok, required := apiKeyRouteAllowed(claims.Scopes, r.Method, r.URL.Path)
			if !ok {
				if required == "" {
					writeError(w, http.StatusForbidden, "this endpoint is not available to API keys")
					return
				}
				writeError(w, http.StatusForbidden, "api key missing required scope: "+required)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
