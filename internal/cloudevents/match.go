package cloudevents

import "path"

// MatchAnyGlob reports whether s matches any of the path.Match glob patterns.
// An empty pattern list returns false. Patterns that fail to compile are
// skipped rather than treated as errors, matching the lenient behaviour the
// CloudEvents transports want: a malformed filter never accidentally matches.
//
// This is the single home for the "type matches any of a list of globs"
// predicate used by the reply dispatcher, the cloudevent_http notify provider,
// and the poll/stream HTTP transports (server-side OR over a filter set).
func MatchAnyGlob(s string, patterns []string) bool {
	for _, p := range patterns {
		if ok, err := path.Match(p, s); err == nil && ok {
			return true
		}
	}
	return false
}
