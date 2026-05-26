package gen

import (
	"strings"
	"testing"
)

// hasLineWith reports whether any line in s contains both a and b.
// Used so struct-field assertions tolerate gofmt's column alignment.
func hasLineWith(s, a, b string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

func TestRenderGoTypes(t *testing.T) {
	cat := &Catalog{Events: []Event{{
		Type: "run.yipyap.alert.fired.v1", Direction: "out",
		Summary: "fired", SubjectTemplate: "alert/{alert_id}",
		Data: []Field{
			{Name: "alert_id", Type: "string", Required: true},
			{Name: "severity", Type: "string", Required: true, Enum: []string{"info", "warning", "critical"}},
		},
	}}}
	out, err := RenderGoTypes(cat, "types")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, needle := range []string{
		"package types",
		"type AlertFiredV1Data struct",
		"const TypeAlertFiredV1 = \"run.yipyap.alert.fired.v1\"",
	} {
		if !strings.Contains(s, needle) {
			t.Errorf("output missing %q\n---\n%s", needle, s)
		}
	}
	for _, pair := range []struct{ name, typ string }{
		{"AlertID", "string"},
		{"Severity", "string"},
	} {
		if !hasLineWith(s, pair.name, pair.typ) {
			t.Errorf("output missing line containing %q and %q\n---\n%s", pair.name, pair.typ, s)
		}
	}
}
