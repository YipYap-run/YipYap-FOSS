package reply

import "encoding/json"

// unmarshalJSON is a tiny wrapper so that registry.go and other callers
// can parse JSON without importing encoding/json directly, keeping their
// imports narrow and making it trivial to swap in a faster parser later.
func unmarshalJSON(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
