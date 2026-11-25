package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

// On a bus without a shared log (plain NATS), each web replica keeps its
// own in-memory log with its own epoch. These tests model two replicas as
// two feeds on one bus.

func replicaPoll(t *testing.T, h *CloudEventsPollHandler, orgID, since string) ([]string, string) {
	t.Helper()
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/poll?"+q.Encode(), nil)
	r.Header.Set(CESourceIDHeader, "replica-test")
	r = r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: orgID}))
	w := httptest.NewRecorder()
	h.Poll(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("poll status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := make([]string, 0, len(body.Events))
	for _, e := range body.Events {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids, body.NextCursor
}

func replicaPublish(t *testing.T, b bus.Bus, orgID string) string {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetID(ulid.Make().String())
	ev.SetType("run.yipyap.alert.fired.v1")
	ev.SetSource("replica-test")
	ev.SetSpecVersion("1.0")
	_ = ev.SetData(ce.ApplicationJSON, map[string]string{"k": "v"})
	if err := cepub.NewPublisher(b).Publish(context.Background(), orgID, ev); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return ev.ID()
}

// publishInOrder publishes n events, waiting after each so every replica
// appends them in the same order. The in-process bus delivers each publish
// on its own goroutine; NATS keeps per-publisher order on its own.
func publishInOrder(t *testing.T, b bus.Bus, orgID string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, replicaPublish(t, b, orgID))
		time.Sleep(20 * time.Millisecond)
	}
	return ids
}

func newReplica(b bus.Bus) *CloudEventsPollHandler {
	h := NewCloudEventsPollHandlerWithFeed(NewCloudEventsFeed(b, nil))
	h.drainWindow = 200 * time.Millisecond
	return h
}

func sortedIDs(ids ...string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func TestCEFeed_CursorMovesBetweenReplicas(t *testing.T) {
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	a, c := newReplica(b), newReplica(b)
	const org = "org-replica"

	_, cur := replicaPoll(t, a, org, "")
	ids := publishInOrder(t, b, org, 2)
	e1, e2 := ids[0], ids[1]

	got, cur := replicaPoll(t, a, org, cur)
	if !equalStrings(got, sortedIDs(e1, e2)) {
		t.Fatalf("replica A got %v, want %v", got, sortedIDs(e1, e2))
	}
	// The next poll lands on the other replica. It must not replay the
	// events A already delivered and the client confirmed.
	got, cur = replicaPoll(t, c, org, cur)
	if len(got) != 0 {
		t.Fatalf("replica B replayed %v after the client confirmed them on A", got)
	}
	e3 := publishInOrder(t, b, org, 1)[0]
	got, cur = replicaPoll(t, c, org, cur)
	if !equalStrings(got, []string{e3}) {
		t.Fatalf("replica B got %v, want [%s]", got, e3)
	}
	// And back again.
	if got, _ = replicaPoll(t, a, org, cur); len(got) != 0 {
		t.Fatalf("replica A replayed %v", got)
	}
}

func TestCEFeed_UnconfirmedCursorRedeliversOnOtherReplica(t *testing.T) {
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	a, c := newReplica(b), newReplica(b)
	const org = "org-replica"

	_, c0 := replicaPoll(t, a, org, "")
	ids := publishInOrder(t, b, org, 2)
	e1, e2 := ids[0], ids[1]
	if got, _ := replicaPoll(t, a, org, c0); len(got) != 2 {
		t.Fatalf("replica A got %v, want 2 events", got)
	}
	// The client never confirmed the batch; its retry with the old cursor
	// on the other replica gets the same events.
	got, _ := replicaPoll(t, c, org, c0)
	if !equalStrings(got, sortedIDs(e1, e2)) {
		t.Fatalf("replica B got %v, want %v", got, sortedIDs(e1, e2))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
