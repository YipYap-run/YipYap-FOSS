package handlers

import (
	"context"
	"fmt"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
)

// newBareReplyEvent constructs a minimal CE for tests that exercise the
// common helpers directly.
func newBareReplyEvent(t *testing.T, typ string) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID("reply-" + typ)
	ev.SetSource("https://consumer.example.com/")
	ev.SetType(typ)
	return ev
}

func TestRequireAlertScopedEntry_RejectsMonitorScoped(t *testing.T) {
	s := newTestStore(t)
	ev := newBareReplyEvent(t, "run.yipyap.reply.alert.suppressed.v1")
	entry := &reply.Entry{OrgID: "org-A", MonitorID: "mon-1"} // AlertID empty
	if err := requireAlertScopedEntry(context.Background(), s, ev, entry, ""); err == nil {
		t.Fatal("expected rejection for monitor-scoped entry on alert-mutating handler")
	}
}

func TestRequireAlertScopedEntry_AcceptsAlertScoped(t *testing.T) {
	s := newTestStore(t)
	ev := newBareReplyEvent(t, "run.yipyap.reply.alert.suppressed.v1")
	entry := &reply.Entry{OrgID: "org-A", AlertID: "alert-1"}
	if err := requireAlertScopedEntry(context.Background(), s, ev, entry, ""); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestLoadAndVerifyAlert_CrossOrgRejected(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ev := newBareReplyEvent(t, "run.yipyap.reply.alert.suppressed.v1")
	// Entry claims a different org than the real alert's.
	entry := &reply.Entry{OrgID: "some-other-org", AlertID: alert.ID}
	if _, err := loadAndVerifyAlert(context.Background(), s, ev, entry, "", alert.ID); err == nil {
		t.Fatal("expected cross_org_alert rejection")
	}
}

func TestLoadAndVerifyAlert_SameOrgAccepted(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)
	ev := newBareReplyEvent(t, "run.yipyap.reply.alert.suppressed.v1")
	entry := &reply.Entry{OrgID: alert.OrgID, AlertID: alert.ID}
	got, err := loadAndVerifyAlert(context.Background(), s, ev, entry, "", alert.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != alert.ID {
		t.Fatalf("got=%+v, want alert %q", got, alert.ID)
	}
}

func TestValidateMatchFilterValues(t *testing.T) {
	// A2-NM-4: filter values are strings (or []string of strings) only.
	// Falsy non-string values (bool, number, [], {}) are no longer
	// silently accepted, Phase-6 consumers should never need to
	// interpret them.
	cases := []struct {
		name    string
		mf      map[string]any
		wantErr bool
	}{
		{"non_empty", map[string]any{"severity": "critical"}, false},
		{"empty_string", map[string]any{"severity": ""}, true},
		{"whitespace", map[string]any{"severity": "   "}, true},
		{"tab_newline", map[string]any{"severity": "\t\n"}, true},
		{"typed_nil", map[string]any{"severity": nil}, true},
		{"bool_value", map[string]any{"severity": true}, true},
		{"bool_false", map[string]any{"severity": false}, true},
		{"number", map[string]any{"p99": 95.5}, true},
		{"int_zero", map[string]any{"p99": 0}, true},
		{"mixed_one_empty", map[string]any{"a": "x", "b": ""}, true},
		{"empty_map", map[string]any{"a": map[string]any{}}, true},
		{"empty_any_slice", map[string]any{"a": []any{}}, true},
		{"any_slice_with_int", map[string]any{"a": []any{"x", 7}}, true},
		{"any_slice_strings", map[string]any{"a": []any{"x", "y"}}, false},
		{"string_slice", map[string]any{"a": []string{"x", "y"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMatchFilterValues(tc.mf)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateMatchFilterValues(%v) err=%v wantErr=%v", tc.mf, err, tc.wantErr)
			}
		})
	}
}

// TestValidateMatchFilterValues_KeyCap verifies A2-NM-3: more than 16 keys
// is rejected.
func TestValidateMatchFilterValues_KeyCap(t *testing.T) {
	mf := map[string]any{}
	for i := 0; i < MaxMatchFilterKeys+1; i++ {
		mf[fmt.Sprintf("k%d", i)] = "v"
	}
	if err := validateMatchFilterValues(mf); err == nil {
		t.Fatal("expected key-count rejection")
	}
}

// TestValidateReplyURL covers the http/https scheme allowlist (A2-NM-2).
func TestValidateReplyURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty", "", false},
		{"https", "https://x.example/y", false},
		{"http", "http://x.example/y", false},
		{"javascript", "javascript:alert(1)", true},
		{"data_html", "data:text/html,<script>alert(1)</script>", true},
		{"file", "file:///etc/passwd", true},
		{"vbscript", "vbscript:msgbox(1)", true},
		{"ftp", "ftp://x.example/", true},
		{"https_no_host", "https://", true},
		{"upper_https", "HTTPS://x.example/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReplyURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateReplyURL(%q) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

// TestTruncateUTF8Runes confirms the rune-aware truncation never splits a
// multi-byte codepoint at the byte cap (Round-1 residual fix).
func TestTruncateUTF8Runes(t *testing.T) {
	// Multi-byte runes: € is 3 bytes in UTF-8 (0xE2 0x82 0xAC).
	// "ab€cd" = a(1) + b(1) + €(3) + c(1) + d(1) = 7 bytes.
	in := "ab€cd"
	if got := truncateUTF8Runes(in, 7); got != in {
		t.Errorf("under-cap mutated: got=%q want=%q", got, in)
	}
	// Cap of 4 must drop the € rune (would be split mid-byte) and stop
	// at "ab" (2 bytes), since adding € would push to 5 bytes which
	// fits if cap=5 but we set cap=4 → only "ab".
	if got := truncateUTF8Runes(in, 4); got != "ab" {
		t.Errorf("cap=4 got=%q want=ab", got)
	}
	if !utf8Valid(truncateUTF8Runes(in, 5)) {
		t.Errorf("cap=5 produced invalid UTF-8")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
