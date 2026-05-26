// Package cloudevents owns yipyap's CloudEvent catalog, generator, and
// shared helpers. The typed event structs in ./types and JSON Schemas
// in ./schema are generated from ./catalog/*.yaml, run `go generate`
// from the repo root after editing any YAML.
//
// Generated artifacts (do not hand-edit):
//   - ./types/types.gen.go
//   - ./schema/*.json
//   - ../../web/src/lib/cloudevents/types.gen.ts
//
//go:generate go run ./gen/cmd/catalog-gen
package cloudevents
