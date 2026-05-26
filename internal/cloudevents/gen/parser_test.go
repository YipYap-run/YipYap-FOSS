package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCatalog_SingleType(t *testing.T) {
	dir := t.TempDir()
	yaml := `
type: run.yipyap.test.v1
direction: out
summary: Test event.
subject_template: "test/{id}"
data:
  - name: id
    type: string
    required: true
`
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := ParseCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(cat.Events))
	}
	ev := cat.Events[0]
	if ev.Type != "run.yipyap.test.v1" {
		t.Errorf("type = %q", ev.Type)
	}
	if ev.Direction != "out" {
		t.Errorf("direction = %q", ev.Direction)
	}
	if len(ev.Data) != 1 || ev.Data[0].Name != "id" {
		t.Errorf("data = %+v", ev.Data)
	}
}

func TestParseCatalog_RejectsUnknownDirection(t *testing.T) {
	dir := t.TempDir()
	yaml := "type: run.yipyap.t.v1\ndirection: sideways\nsummary: x\nsubject_template: \"t\"\ndata: []\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(dir); err == nil {
		t.Fatal("want error for unknown direction, got nil")
	}
}

func TestParseCatalog_RejectsDuplicateType(t *testing.T) {
	dir := t.TempDir()
	yaml := "type: run.yipyap.dup.v1\ndirection: out\nsummary: x\nsubject_template: \"t\"\ndata: []\n"
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseCatalog(dir)
	if err == nil {
		t.Fatal("want error for duplicate type, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.yaml") || !strings.Contains(msg, "b.yaml") {
		t.Errorf("error should name both files, got: %v", err)
	}
}

func TestParseCatalog_RejectsMissingType(t *testing.T) {
	dir := t.TempDir()
	yaml := "direction: out\nsummary: x\nsubject_template: \"t\"\ndata: []\n"
	if err := os.WriteFile(filepath.Join(dir, "notype.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseCatalog(dir)
	if err == nil {
		t.Fatal("want error for missing type, got nil")
	}
	if !strings.Contains(err.Error(), "type is required") {
		t.Errorf("want 'type is required' in error, got: %v", err)
	}
}

func TestParseCatalog_RejectsMissingDirection(t *testing.T) {
	dir := t.TempDir()
	yaml := "type: run.yipyap.nodir.v1\nsummary: x\nsubject_template: \"t\"\ndata: []\n"
	if err := os.WriteFile(filepath.Join(dir, "nodir.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseCatalog(dir)
	if err == nil {
		t.Fatal("want error for missing direction, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "direction is required") {
		t.Errorf("want 'direction is required' in error, got: %v", err)
	}
	if strings.Contains(msg, "unknown direction") {
		t.Errorf("missing direction should NOT produce 'unknown direction' error, got: %v", err)
	}
}

func TestParseCatalog_RejectsUnknownYAMLField(t *testing.T) {
	dir := t.TempDir()
	yaml := "type: run.yipyap.t.v1\ndirection: out\nsummary: x\nsubjectTemplate: \"t\"\ndata: []\n"
	if err := os.WriteFile(filepath.Join(dir, "typo.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(dir); err == nil {
		t.Fatal("want error for unknown YAML field, got nil")
	}
}

func TestParseCatalog_RejectsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := ParseCatalog(dir)
	if err == nil {
		t.Fatal("want error for empty dir, got nil")
	}
	if !strings.Contains(err.Error(), "no *.yaml files") {
		t.Errorf("want 'no *.yaml files' in error, got: %v", err)
	}
}
