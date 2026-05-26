package gen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderJSONSchema(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.x.v1", Direction: "out", Summary: "x",
		SubjectTemplate: "x/{id}",
		Data:            []Field{{Name: "id", Type: "string", Required: true}},
	}}}
	files, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := files["run.yipyap.x.v1.json"]
	if !ok {
		t.Fatalf("missing schema file, have: %v", files)
	}
	if !strings.Contains(string(got), `"$schema"`) {
		t.Errorf("schema missing $schema key")
	}
}

func TestRenderJSONSchemas_MultipleEvents(t *testing.T) {
	cat := &Catalog{Events: []Event{
		{
			Type: "run.yipyap.a.v1", Direction: "out", Summary: "a",
			SubjectTemplate: "a/{id}",
			Data:            []Field{{Name: "id", Type: "string", Required: true}},
		},
		{
			Type: "run.yipyap.b.v1", Direction: "out", Summary: "b",
			SubjectTemplate: "b/{id}",
			Data:            []Field{{Name: "id", Type: "string", Required: true}},
		},
	}}
	files, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	for _, tc := range []struct {
		file string
		id   string
	}{
		{"run.yipyap.a.v1.json", "https://console.yipyap.run/schemas/cloudevents/run.yipyap.a.v1.json"},
		{"run.yipyap.b.v1.json", "https://console.yipyap.run/schemas/cloudevents/run.yipyap.b.v1.json"},
	} {
		raw, ok := files[tc.file]
		if !ok {
			t.Fatalf("missing %s", tc.file)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.file, err)
		}
		if got := obj["$id"]; got != tc.id {
			t.Errorf("%s: $id = %v, want %s", tc.file, got, tc.id)
		}
	}
}

func TestRenderJSONSchemas_FieldFormats(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.f.v1", Direction: "out", Summary: "f",
		SubjectTemplate: "f/{id}",
		Data: []Field{
			{Name: "when", Type: "string", Required: true, Format: "rfc3339"},
			{Name: "id", Type: "string", Required: true, Format: "ulid"},
			{Name: "link", Type: "string", Format: "uri"},
			{Name: "severity", Type: "string", Required: true, Enum: []string{"info", "warning"}},
			{Name: "count", Type: "int", Required: true},
			{Name: "ok", Type: "bool"},
			{Name: "meta", Type: "object", Description: "arbitrary metadata"},
			{Name: "tags", Type: "array", Items: &Field{Name: "", Type: "string"}},
			{Name: "raw", Type: "array"},
		},
	}}}
	files, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	raw := files["run.yipyap.f.v1.json"]
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	props := envelope["properties"].(map[string]any)
	dataSchema := props["data"].(map[string]any)
	dataProps := dataSchema["properties"].(map[string]any)

	get := func(name string) map[string]any {
		v, ok := dataProps[name].(map[string]any)
		if !ok {
			t.Fatalf("missing field %q in data properties", name)
		}
		return v
	}

	// when: rfc3339 → string + format date-time
	when := get("when")
	if when["type"] != "string" || when["format"] != "date-time" {
		t.Errorf("when: %#v", when)
	}
	// id: ulid → string + pattern
	idField := get("id")
	if idField["type"] != "string" || idField["pattern"] != "^[0-9A-HJKMNP-TV-Z]{26}$" {
		t.Errorf("id: %#v", idField)
	}
	// link: uri → string + format uri
	link := get("link")
	if link["type"] != "string" || link["format"] != "uri" {
		t.Errorf("link: %#v", link)
	}
	// severity: enum
	sev := get("severity")
	enum, ok := sev["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "info" || enum[1] != "warning" {
		t.Errorf("severity enum: %#v", sev)
	}
	// count: int → integer
	count := get("count")
	if count["type"] != "integer" {
		t.Errorf("count: %#v", count)
	}
	// ok: bool → boolean
	okField := get("ok")
	if okField["type"] != "boolean" {
		t.Errorf("ok: %#v", okField)
	}
	// meta: object + description
	meta := get("meta")
	if meta["type"] != "object" || meta["description"] != "arbitrary metadata" {
		t.Errorf("meta: %#v", meta)
	}
	// tags: array with items string
	tags := get("tags")
	if tags["type"] != "array" {
		t.Errorf("tags type: %#v", tags)
	}
	items, ok := tags["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Errorf("tags items: %#v", tags["items"])
	}
	// raw: array without items
	rawField := get("raw")
	if rawField["type"] != "array" {
		t.Errorf("raw: %#v", rawField)
	}
	if _, hasItems := rawField["items"]; hasItems {
		t.Errorf("raw should have no items key: %#v", rawField)
	}

	// required at data level should contain when, id, severity, count (and not link/ok/meta/tags/raw)
	required, _ := dataSchema["required"].([]any)
	reqSet := map[string]bool{}
	for _, r := range required {
		reqSet[r.(string)] = true
	}
	for _, want := range []string{"when", "id", "severity", "count"} {
		if !reqSet[want] {
			t.Errorf("data required missing %q, have %v", want, required)
		}
	}
	for _, notWant := range []string{"link", "ok", "meta", "tags", "raw"} {
		if reqSet[notWant] {
			t.Errorf("data required should not contain %q", notWant)
		}
	}
}

func TestRenderJSONSchemas_Deterministic(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.d.v1", Direction: "out", Summary: "d",
		SubjectTemplate: "d/{id}",
		Data: []Field{
			{Name: "id", Type: "string", Required: true, Format: "ulid"},
			{Name: "severity", Type: "string", Required: true, Enum: []string{"a", "b", "c"}},
			{Name: "count", Type: "int"},
		},
	}}}
	a, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	for name, av := range a {
		bv := b[name]
		if !bytes.Equal(av, bv) {
			t.Errorf("non-deterministic output for %s:\n--- a ---\n%s\n--- b ---\n%s", name, av, bv)
		}
	}
}

func TestRenderJSONSchemas_DataAdditionalPropertiesFalse(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.ap.v1", Direction: "out", Summary: "ap",
		SubjectTemplate: "ap/{id}",
		Data:            []Field{{Name: "id", Type: "string", Required: true}},
	}}}
	files, err := RenderJSONSchemas(cat)
	if err != nil {
		t.Fatal(err)
	}
	raw := files["run.yipyap.ap.v1.json"]
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	props := envelope["properties"].(map[string]any)
	dataSchema := props["data"].(map[string]any)
	if ap, ok := dataSchema["additionalProperties"].(bool); !ok || ap != false {
		t.Errorf("data.additionalProperties should be false, got %#v", dataSchema["additionalProperties"])
	}
	// envelope-level additionalProperties should be true
	if ap, ok := envelope["additionalProperties"].(bool); !ok || ap != true {
		t.Errorf("envelope.additionalProperties should be true, got %#v", envelope["additionalProperties"])
	}
	// Trailing newline.
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Errorf("schema bytes should end with newline")
	}
}
