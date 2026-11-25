package middleware

import (
	"net/http"
	"sort"
	"strings"
)

// API key scopes use one vocabulary: "<resource>:read" and "<resource>:write".
// A write scope implies read on the same resource. A bare "<resource>" (the
// pre-vocabulary format) is still honored on stored keys and means write;
// new keys are normalized to the canonical form at creation.
//
// A key with an empty scope list is unrestricted on scoped routes (legacy
// behaviour, also the "full access" preset). Every key, scoped or not, is
// still kept off account and session routes by RequireSession and off
// admin-only routes by RequireRole, since its role is "api_key".

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// ScopeResource is one entry in the API key scope vocabulary.
type ScopeResource struct {
	Resource string `json:"resource"`
	Label    string `json:"label"`
	Read     string `json:"read"`
	Write    string `json:"write"`
	// ReadOnly marks a resource whose writes are owner/admin-only. A key's
	// role is api_key, so its write scope never reaches a write route and a
	// client should offer only the read scope.
	ReadOnly bool `json:"read_only"`
}

// coreScopeResources are the resources gated by RequireScope in every edition.
var coreScopeResources = []ScopeResource{
	resource("monitors", "Monitors, groups and states"),
	resource("alerts", "Alerts"),
	resource("escalation_policies", "Escalation policies"),
	resource("notification_channels", "Notification channels"),
	resource("maintenance_windows", "Maintenance windows"),
	readOnlyResource("users", "Members"),
	readOnlyResource("org", "Organization profile and settings"),
}

func resource(name, label string) ScopeResource {
	return ScopeResource{Resource: name, Label: label, Read: name + ":" + ScopeRead, Write: name + ":" + ScopeWrite}
}

func readOnlyResource(name, label string) ScopeResource {
	r := resource(name, label)
	r.ReadOnly = true
	return r
}

// ScopeVocabulary returns every resource an API key can be scoped to in this
// build, in display order.
func ScopeVocabulary() []ScopeResource {
	out := make([]ScopeResource, 0, len(coreScopeResources)+len(editionScopeResources))
	out = append(out, coreScopeResources...)
	out = append(out, editionScopeResources...)
	return out
}

// AllowedScopes returns the flat, sorted list of canonical scope strings.
func AllowedScopes() []string {
	var out []string
	for _, r := range ScopeVocabulary() {
		out = append(out, r.Read, r.Write)
	}
	sort.Strings(out)
	return out
}

// NormalizeScopes validates requested scopes against the vocabulary. It
// trims and lowercases each entry, maps a bare resource name to its write
// scope, and drops duplicates. It returns the canonical list plus any
// entries it did not recognize.
func NormalizeScopes(in []string) (canonical []string, unknown []string) {
	known := map[string]bool{}
	resources := map[string]bool{}
	for _, r := range ScopeVocabulary() {
		known[r.Read] = true
		known[r.Write] = true
		resources[r.Resource] = true
	}
	seen := map[string]bool{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if resources[s] {
			s += ":" + ScopeWrite
		}
		if !known[s] {
			unknown = append(unknown, raw)
			continue
		}
		if !seen[s] {
			seen[s] = true
			canonical = append(canonical, s)
		}
	}
	return canonical, unknown
}

// RequireScope returns middleware that enforces API key scope restrictions
// for one resource. Safe methods (GET, HEAD, OPTIONS) need "<resource>:read"
// or "<resource>:write"; other methods need "<resource>:write".
//
// It applies only to API key claims with a non-empty scope list. Human
// sessions pass through so the role system stays their only gate.
func RequireScope(resource string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			if claims.Role == "api_key" {
				action := actionForMethod(r.Method)
				if !scopeGrants(claims.Scopes, resource, action) {
					writeError(w, http.StatusForbidden, "api key missing required scope: "+resource+":"+action)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireSession rejects API keys outright. It guards account and session
// routes (password, email, MFA factors, linked identities, session refresh)
// where a key acting as its creator would be an escalation.
func RequireSession() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if claims.Role == "api_key" {
				writeError(w, http.StatusForbidden, "api keys cannot access this endpoint")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireWriteAccess blocks API keys that only carry read-type scopes from
// performing any mutating HTTP method (POST, PUT, PATCH, DELETE).
//
// It does this by checking whether ALL declared scopes end in ":read" or equal
// "read".  If so, the key is considered read-only and write methods are
// rejected.  A key with no scopes, or with at least one write scope, passes
// through.
//
// Regular JWT sessions are never affected.
func RequireWriteAccess() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			if claims.Role == "api_key" && len(claims.Scopes) > 0 && isReadOnly(claims.Scopes) {
				writeError(w, http.StatusForbidden, "api key is read-only")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// isReadOnly reports whether every scope in the list is a read-type scope
// (ends in ":read", or is "read-only" or the legacy "read").
func isReadOnly(scopes []string) bool {
	for _, s := range scopes {
		if s != "read" && s != ScopePresetReadOnly && !strings.HasSuffix(s, ":read") {
			return false
		}
	}
	return true
}
