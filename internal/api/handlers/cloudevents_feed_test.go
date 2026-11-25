package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
)

func TestDecodeCECursor(t *testing.T) {
	c, err := decodeCECursor(encodeCECursor("ep", 42))
	if err != nil || c == nil || c.E != "ep" || c.S != 42 {
		t.Fatalf("round trip = %+v, %v", c, err)
	}
	if c, err := decodeCECursor(""); c != nil || err != nil {
		t.Fatalf("empty = %+v, %v", c, err)
	}
	// Counter cursor from the old handler: no position, not an error.
	if c, err := decodeCECursor("eyJzZXEiOjd9"); c != nil || err != nil {
		t.Fatalf("legacy = %+v, %v", c, err)
	}
	long := make([]byte, ceMaxCursorLen+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"!!", "bm9wZQ", string(long), "eyJ2IjoyLCJzIjoxfQ"} {
		if _, err := decodeCECursor(bad); err == nil {
			t.Errorf("decode(%q) accepted", bad)
		}
	}
}

func TestCECursorAnchorFields(t *testing.T) {
	in := ceCursor{E: "ep", S: 7, T: 1700000000123, I: "01JABCDEF"}
	c, err := decodeCECursor(in.encode())
	if err != nil || c == nil || c.S != 7 || c.T != in.T || c.I != in.I {
		t.Fatalf("round trip = %+v, %v", c, err)
	}
	// An ID that JSON escaping blows up past the cursor limit is dropped
	// rather than producing a cursor the server would reject.
	long := ceCursor{E: "ep", S: 1, T: 1, I: strings.Repeat("\x01", ceMaxAnchorIDLen)}
	enc := long.encode()
	c, err = decodeCECursor(enc)
	if err != nil || c == nil || c.I != "" || c.S != 1 {
		t.Fatalf("escaped id cursor = %+v, %v (len %d)", c, err, len(enc))
	}
	for _, bad := range []ceCursor{{E: "ep", T: -1}, {E: "ep", I: strings.Repeat("a", ceMaxAnchorIDLen+1)}} {
		bad.V = ceCursorVersion
		b, _ := json.Marshal(bad)
		if _, err := decodeCECursor(base64.RawURLEncoding.EncodeToString(b)); err == nil {
			t.Errorf("decode accepted %+v", bad)
		}
	}
}

func TestWithCursor(t *testing.T) {
	out := withCursor([]byte(`{"id":"1","type":"t"}`+"\n"), "c1")
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if m["yipyapcursor"] != "c1" || m["id"] != "1" {
		t.Fatalf("got %v", m)
	}
	if got := string(withCursor([]byte(`{}`), "c")); got != `{"yipyapcursor":"c"}` {
		t.Fatalf("empty object: %s", got)
	}
	if got := string(withCursor([]byte(`[1]`), "c")); got != `[1]` {
		t.Fatalf("non-object changed: %s", got)
	}
}

func TestCEPositionStore_CapAndTTL(t *testing.T) {
	s := newCEPositionStore(3, time.Hour)
	now := time.Now()
	s.clock = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		s.set("org", fmt.Sprintf("k%d", i), ceCursor{E: "e", S: uint64(i)})
	}
	if n := s.size("org"); n != 3 {
		t.Fatalf("size = %d, want cap 3", n)
	}
	if _, ok := s.get("org", "k0"); ok {
		t.Fatal("least recently used entry should be evicted")
	}
	if c, ok := s.get("org", "k4"); !ok || c.S != 4 {
		t.Fatalf("k4 = %+v %v", c, ok)
	}
	// Another org has its own budget.
	s.set("other", "k0", ceCursor{E: "e", S: 1})
	if n := s.size("org"); n != 3 {
		t.Fatalf("other org evicted entries: size %d", n)
	}
	now = now.Add(2 * time.Hour)
	if _, ok := s.get("org", "k4"); ok {
		t.Fatal("expired entry returned")
	}
}

// TestCEFeed_RejectsWildcardOrgID guards org isolation: the reader filters
// events.cloudevents.fanout.<org>.>, so an org ID with a wildcard or dot
// would match other orgs' events.
func TestCEFeed_RejectsWildcardOrgID(t *testing.T) {
	feed := NewCloudEventsFeed(bus.NewChannel(), nil)
	h := NewCloudEventsPollHandlerWithFeed(feed)
	for _, org := range []string{"*", ">", "org-1.x", "org 1", "org-*"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/poll", nil)
		r = r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: org}))
		w := httptest.NewRecorder()
		h.Poll(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("org %q: status %d, want 500", org, w.Code)
		}
	}
	mem, ok := feed.positions.(*cePositionStore)
	if !ok {
		t.Fatal("nil store should give the in-memory position backend")
	}
	if n := mem.size("*"); n != 0 {
		t.Fatalf("rejected org left %d positions", n)
	}
}

func TestCEFeed_UnavailableLogIs503(t *testing.T) {
	feed := newCloudEventsFeedWithLog(unavailableLog{}, nil)
	h := NewCloudEventsPollHandlerWithFeed(feed)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/poll", nil)
	r.Header.Set(CESourceIDHeader, "unavailable-log-test")
	r = r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: "org-1"}))
	w := httptest.NewRecorder()
	h.Poll(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d retry-after %q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestCEMatcher_SeverityUnknownIsDelivered(t *testing.T) {
	feed := newCloudEventsFeedWithLog(unavailableLog{}, nil)
	req := &ceRequest{orgID: "o", filter: ceFilter{severities: map[string]bool{"critical": true}}}
	m := feed.matcher(req)
	ev := []byte(`{"specversion":"1.0","id":"1","type":"run.yipyap.alert.resolved.v1","data":{"alert_id":"gone"}}`)
	if !m.match(context.Background(), ev) {
		t.Fatal("alert event with unknown severity must be delivered")
	}
	bad := []byte(`{"id":"1","type":"x"}`)
	if m.match(context.Background(), bad) {
		t.Fatal("event without specversion must be dropped")
	}
}
