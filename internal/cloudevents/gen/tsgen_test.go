package gen

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTSTypes(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.alert.fired.v1", Direction: "out",
		Summary: "x", SubjectTemplate: "alert/{id}",
		Data: []Field{
			{Name: "alert_id", Type: "string", Required: true, Format: "ulid"},
			{Name: "severity", Type: "string", Required: true, Enum: []string{"info", "warning", "critical"}},
			{Name: "retries", Type: "int", Required: false},
		},
	}}}
	out, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, needle := range []string{
		`export const TypeAlertFiredV1 = "run.yipyap.alert.fired.v1";`,
		`export interface AlertFiredV1Data {`,
		`alert_id: string;`,
		`severity: "info" | "warning" | "critical";`,
		`retries?: number;`,
		`DO NOT EDIT`,
	} {
		if !strings.Contains(s, needle) {
			t.Errorf("output missing %q\n---\n%s", needle, s)
		}
	}
}

func TestRenderTSTypes_EmptyCatalog(t *testing.T) {
	cat := &Catalog{}
	out, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "DO NOT EDIT") {
		t.Errorf("missing header:\n%s", s)
	}
	if strings.Contains(s, "export const") || strings.Contains(s, "export interface") {
		t.Errorf("empty catalog should not emit exports:\n%s", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("output must end with newline")
	}
}

func TestRenderTSTypes_ArrayAndObject(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.sample.v1", Direction: "out",
		Summary: "x", SubjectTemplate: "s/{id}",
		Data: []Field{
			{Name: "tags", Type: "array", Required: true, Items: &Field{Type: "string"}},
			{Name: "meta", Type: "object", Required: true},
			{Name: "bare_list", Type: "array", Required: false},
		},
	}}}
	out, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, needle := range []string{
		`tags: string[];`,
		`meta: Record<string, unknown>;`,
		`bare_list?: unknown[];`,
	} {
		if !strings.Contains(s, needle) {
			t.Errorf("output missing %q\n---\n%s", needle, s)
		}
	}
}

func TestRenderTSTypes_Deterministic(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.alert.fired.v1", Direction: "out",
		Summary: "x", SubjectTemplate: "alert/{id}",
		Data: []Field{
			{Name: "alert_id", Type: "string", Required: true, Format: "ulid"},
			{Name: "severity", Type: "string", Required: true, Enum: []string{"info", "warning", "critical"}},
		},
	}}}
	a, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("non-deterministic output:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

func TestRenderTSTypes_NestedArray(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.matrix.v1", Direction: "out",
		Summary: "x", SubjectTemplate: "m/{id}",
		Data: []Field{
			{Name: "grid", Type: "array", Required: true, Items: &Field{
				Type:  "array",
				Items: &Field{Type: "string"},
			}},
		},
	}}}
	out, err := RenderTSTypes(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `grid: string[][];`) {
		t.Errorf("output missing nested array type:\n%s", s)
	}
}

func TestRenderTSTypes_DuplicateField(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.dup.v1", Direction: "out",
		Summary: "x", SubjectTemplate: "d/{id}",
		Data: []Field{
			{Name: "x", Type: "string", Required: true},
			{Name: "x", Type: "int", Required: true},
		},
	}}}
	if _, err := RenderTSTypes(cat); err == nil {
		t.Errorf("expected error for duplicate field name")
	}
}
