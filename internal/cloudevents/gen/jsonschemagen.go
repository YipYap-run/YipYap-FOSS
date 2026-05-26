package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// schemaIDPrefix is the canonical URL prefix for published event schemas.
const schemaIDPrefix = "https://console.yipyap.run/schemas/cloudevents/"

// ulidPattern is the Crockford base32 ULID regex used for the `id` envelope
// field and any catalog field with format: ulid.
const ulidPattern = "^[0-9A-HJKMNP-TV-Z]{26}$"

// orderedMap is a tiny ordered-keys map that serializes to a JSON object with
// deterministic key order. We need this because JSON Schema readers (and human
// reviewers) expect a conventional key order (`$schema`, `$id`, `title`, ...),
// and Go's encoding/json alphabetizes map keys.
type orderedMap struct {
	keys   []string
	values map[string]any
}

func newOrderedMap() *orderedMap {
	return &orderedMap{values: map[string]any{}}
}

func (m *orderedMap) set(k string, v any) {
	if _, ok := m.values[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.values[k] = v
}

func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(m.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// fieldSchema renders one catalog field as a JSON Schema fragment.
func fieldSchema(f Field) *orderedMap {
	m := newOrderedMap()
	switch f.Type {
	case "string":
		m.set("type", "string")
		switch f.Format {
		case "rfc3339":
			m.set("format", "date-time")
		case "ulid":
			m.set("pattern", ulidPattern)
		case "uri":
			m.set("format", "uri")
		}
		if len(f.Enum) > 0 {
			// Copy the slice so callers can't mutate the rendered schema.
			enum := make([]string, len(f.Enum))
			copy(enum, f.Enum)
			m.set("enum", enum)
		}
	case "int":
		m.set("type", "integer")
	case "bool":
		m.set("type", "boolean")
	case "object":
		m.set("type", "object")
	case "array":
		m.set("type", "array")
		if f.Items != nil {
			m.set("items", fieldSchema(*f.Items))
		}
	default:
		// Unknown types render as empty `{}`, the parser should have rejected
		// them, but we don't want to panic here.
	}
	if f.Description != "" {
		m.set("description", f.Description)
	}
	return m
}

// dataSchema renders the nested `data` object for one event.
func dataSchema(ev Event) *orderedMap {
	m := newOrderedMap()
	m.set("type", "object")

	props := newOrderedMap()
	var required []string
	for _, f := range ev.Data {
		props.set(f.Name, fieldSchema(f))
		if f.Required {
			required = append(required, f.Name)
		}
	}
	m.set("properties", props)
	if len(required) > 0 {
		m.set("required", required)
	}
	m.set("additionalProperties", false)
	return m
}

// envelopeSchema renders the full CloudEvent envelope schema for one event.
func envelopeSchema(ev Event) *orderedMap {
	m := newOrderedMap()
	m.set("$schema", "https://json-schema.org/draft/2020-12/schema")
	m.set("$id", schemaIDPrefix+ev.Type+".json")
	m.set("title", ev.Type)
	m.set("description", ev.Summary)
	m.set("type", "object")

	props := newOrderedMap()

	specversion := newOrderedMap()
	specversion.set("const", "1.0")
	props.set("specversion", specversion)

	idField := newOrderedMap()
	idField.set("type", "string")
	idField.set("pattern", ulidPattern)
	props.set("id", idField)

	source := newOrderedMap()
	source.set("type", "string")
	source.set("format", "uri")
	props.set("source", source)

	typeField := newOrderedMap()
	typeField.set("const", ev.Type)
	props.set("type", typeField)

	subject := newOrderedMap()
	subject.set("type", "string")
	props.set("subject", subject)

	timeField := newOrderedMap()
	timeField.set("type", "string")
	timeField.set("format", "date-time")
	props.set("time", timeField)

	props.set("data", dataSchema(ev))

	m.set("properties", props)
	m.set("required", []string{"specversion", "id", "source", "type", "time", "data"})
	m.set("additionalProperties", true)
	return m
}

// RenderJSONSchemas renders one JSON Schema 2020-12 document per catalog event,
// keyed by "{type}.json". Each schema describes the full CloudEvent envelope
// with a nested `data` schema derived from the event's Data fields.
//
// The function is pure (no I/O) and deterministic: repeated invocations on the
// same catalog produce byte-equal output.
func RenderJSONSchemas(cat *Catalog) (map[string][]byte, error) {
	out := make(map[string][]byte, len(cat.Events))
	for _, ev := range cat.Events {
		schema := envelopeSchema(ev)
		buf, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal schema for %s: %w", ev.Type, err)
		}
		buf = append(buf, '\n')
		out[ev.Type+".json"] = buf
	}
	return out, nil
}
