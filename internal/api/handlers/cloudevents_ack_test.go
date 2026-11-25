package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// postAck sends POST /api/v1/cloudevents/ack and returns the status.
func postAck(t *testing.T, h *pollTestHarness, bearer, sourceID string, body any) int {
	t.Helper()
	var buf bytes.Buffer
	switch b := body.(type) {
	case string:
		buf.WriteString(b)
	default:
		if err := json.NewEncoder(&buf).Encode(b); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, h.ts.URL+"/api/v1/cloudevents/ack", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if sourceID != "" {
		req.Header.Set("X-YipYap-Source-ID", sourceID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// A stream client confirms progress with the ack endpoint while staying
// connected. A later read without a cursor resumes after the acked event,
// and events after it are delivered again.
func TestAck_StreamConfirmationMovesRememberedPosition(t *testing.T) {
	h := setupPollTest(t)
	const src = "stream-ack-src"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.ts.URL+"/api/v1/cloudevents/stream", nil)
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("X-YipYap-Source-ID", src)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1")
	}
	lines := readNLines(t, resp, 3, 3*time.Second)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	first := eventField(t, []byte(lines[0]), "yipyapcursor")
	want := []string{eventField(t, []byte(lines[1]), "id"), eventField(t, []byte(lines[2]), "id")}

	if code := postAck(t, h, h.apiKey, src, map[string]string{"cursor": first}); code != http.StatusNoContent {
		t.Fatalf("ack status %d, want 204", code)
	}
	cancel()

	// The client restarts and lost its cursor: it gets only what it did
	// not ack.
	_, got, _ := pollWith(t, h, pollOpts{sourceID: src})
	if !sameSet(got, want) {
		t.Fatalf("after ack got %v, want %v", got, want)
	}
	// Other sources are not affected by this source's ack.
	if _, got, _ := pollWith(t, h, pollOpts{sourceID: "other-src"}); len(got) != 0 {
		t.Fatalf("new source got %v, want nothing (starts at end)", got)
	}
}

func TestAck_Validation(t *testing.T) {
	h := setupPollTest(t)
	_, _, c := pollWith(t, h, pollOpts{sourceID: "s1"})

	cases := []struct {
		name   string
		bearer string
		source string
		body   any
		want   int
	}{
		{"no auth", "", "s1", map[string]string{"cursor": c}, http.StatusUnauthorized},
		{"no source id", h.apiKey, "", map[string]string{"cursor": c}, http.StatusBadRequest},
		{"bad source id", h.apiKey, "bad id", map[string]string{"cursor": c}, http.StatusBadRequest},
		{"missing cursor", h.apiKey, "s1", map[string]string{}, http.StatusBadRequest},
		{"bad cursor", h.apiKey, "s1", map[string]string{"cursor": "%%%"}, http.StatusBadRequest},
		{"legacy cursor", h.apiKey, "s1", map[string]string{"cursor": "eyJzZXEiOjd9"}, http.StatusBadRequest},
		{"not json", h.apiKey, "s1", "nope", http.StatusBadRequest},
		{"unknown field", h.apiKey, "s1", `{"cursor":"` + c + `","x":1}`, http.StatusBadRequest},
		{"trailing data", h.apiKey, "s1", `{"cursor":"` + c + `"}{"cursor":"x"}`, http.StatusBadRequest},
		{"too large", h.apiKey, "s1", `{"cursor":"` + strings.Repeat("a", 9000) + `"}`, http.StatusBadRequest},
		{"ok", h.apiKey, "s1", map[string]string{"cursor": c}, http.StatusNoContent},
	}
	for _, tc := range cases {
		if code := postAck(t, h, tc.bearer, tc.source, tc.body); code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, code, tc.want)
		}
	}
}
