package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// pollTestHarness bundles the objects a poll-endpoint test needs to exercise
// the route end-to-end: an httptest server, the shared bus the server is
// hooked up to, the seeded org, and a bearer API key for that org.
type pollTestHarness struct {
	ts       *httptest.Server
	store    *sqlite.SQLiteStore
	msgBus   bus.Bus
	org      *domain.Org
	apiKey   string
	otherOrg *domain.Org
	otherKey string
}

func setupPollTest(t *testing.T) *pollTestHarness {
	t.Helper()

	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	msgBus := bus.NewChannel()
	t.Cleanup(func() { _ = msgBus.Close() })

	hasher := auth.NewAPIKeyHasher([]byte("poll-test-hasher-secret"))
	jwt := auth.NewJWTIssuer([]byte("poll-test-jwt-secret"), time.Hour)

	handler := api.NewServer(s, jwt, msgBus, nil, nil, "", "", api.ServerOptions{
		APIKeyHasher: hasher,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	mkOrg := func(prefix string) (*domain.Org, string) {
		org := &domain.Org{
			ID:   prefix + "-" + ulid.Make().String(),
			Name: "Poll Test Org " + prefix,
			Slug: strings.ToLower("slug-" + prefix + "-" + ulid.Make().String()),
		}
		if err := s.Orgs().Create(ctx, org); err != nil {
			t.Fatalf("create org: %v", err)
		}
		plaintext, hash, kPrefix, err := hasher.GenerateAPIKey()
		if err != nil {
			t.Fatalf("generate api key: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		apiKey := &domain.APIKey{
			ID:        "key-" + ulid.Make().String(),
			OrgID:     org.ID,
			Name:      "poll test key " + prefix,
			KeyHash:   hash,
			Prefix:    kPrefix,
			Scopes:    []string{"monitors:read"},
			CreatedBy: "user-" + prefix,
			CreatedAt: now,
		}
		if err := s.APIKeys().Create(ctx, apiKey); err != nil {
			t.Fatalf("create api key: %v", err)
		}
		return org, plaintext
	}

	org, key := mkOrg("org-poll")
	other, otherKey := mkOrg("org-poll-other")

	return &pollTestHarness{
		ts:       ts,
		store:    s,
		msgBus:   msgBus,
		org:      org,
		apiKey:   key,
		otherOrg: other,
		otherKey: otherKey,
	}
}

// publishTestEvent constructs, marshals, and publishes an event on the bus
// using the same subject convention as internal/cloudevents.Publisher.
func publishTestEvent(t *testing.T, b bus.Bus, orgID, ceType string) ce.Event {
	t.Helper()
	ev := ce.NewEvent()
	ev.SetID(ulid.Make().String())
	ev.SetType(ceType)
	ev.SetSource("poll-test")
	ev.SetSpecVersion("1.0")
	if err := ev.SetData(ce.ApplicationJSON, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("set data: %v", err)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	subject := cepub.SubjectFor(orgID, ceType)
	if err := b.PublishDurable(context.Background(), subject, payload, ev.ID()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return ev
}

// pollResponse matches the JSON shape emitted by the poll endpoint.
type pollResponse struct {
	Events     []json.RawMessage `json:"events"`
	NextCursor string            `json:"next_cursor"`
}

func doPoll(t *testing.T, h *pollTestHarness, bearer, filter, since string, max int) (*http.Response, pollResponse) {
	t.Helper()
	var filters []string
	if filter != "" {
		filters = []string{filter}
	}
	return doPollFilters(t, h, bearer, filters, since, max)
}

// doPollFilters is like doPoll but sends zero or more `filter` query params,
// one Add per value, mirroring how a Knative source forwards every entry in
// spec.filter.types.
func doPollFilters(t *testing.T, h *pollTestHarness, bearer string, filters []string, since string, max int) (*http.Response, pollResponse) {
	t.Helper()
	u := h.ts.URL + "/api/v1/cloudevents/poll"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	q := req.URL.Query()
	for _, f := range filters {
		q.Add("filter", f)
	}
	if since != "" {
		q.Set("since", since)
	}
	if max > 0 {
		q.Set("max", itoa(max))
	}
	req.URL.RawQuery = q.Encode()
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var body pollResponse
	if resp.StatusCode == http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode: %v (raw=%q)", err, string(raw))
		}
		// Leave resp.Body drained; test no longer reads from it.
		resp.Body = io.NopCloser(strings.NewReader(""))
	}
	return resp, body
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestPoll_ReturnsBufferedEvents: publish 3 events for org-1; poll returns them.
func TestPoll_ReturnsBufferedEvents(t *testing.T) {
	h := setupPollTest(t)

	// Subscribe the durable consumer first so the in-memory bus retains
	// messages we publish afterwards. The poll handler creates the
	// consumer on its first call; but our test bus is a fire-and-forget
	// ChannelBus, so messages published BEFORE a subscription exists are
	// dropped. We trigger consumer creation via a priming poll.
	_, _ = doPoll(t, h, h.apiKey, "", "", 0)

	for i := 0; i < 3; i++ {
		publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	}

	// Allow goroutine delivery.
	time.Sleep(100 * time.Millisecond)

	resp, body := doPoll(t, h, h.apiKey, "", "", 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(body.Events) != 3 {
		t.Fatalf("want 3 events, got %d", len(body.Events))
	}
	if body.NextCursor == "" {
		t.Fatalf("next_cursor must be non-empty on success")
	}
}

// TestPoll_FilterGlob: 2 alert events + 1 monitor event; filter returns 2.
func TestPoll_FilterGlob(t *testing.T) {
	h := setupPollTest(t)

	_, _ = doPoll(t, h, h.apiKey, "run.yipyap.alert.*", "", 0)

	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.resolved")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.monitor.up")

	time.Sleep(100 * time.Millisecond)

	resp, body := doPoll(t, h, h.apiKey, "run.yipyap.alert.*", "", 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(body.Events) != 2 {
		t.Fatalf("want 2 alert events after filter, got %d", len(body.Events))
	}
	for _, raw := range body.Events {
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if !strings.HasPrefix(env.Type, "run.yipyap.alert.") {
			t.Fatalf("non-alert event slipped through filter: %s", env.Type)
		}
	}
}

// TestPoll_MultiFilterGlob: with two globs, events matching EITHER are kept
// and non-matching types are dropped (server-side OR over spec.filter.types).
func TestPoll_MultiFilterGlob(t *testing.T) {
	h := setupPollTest(t)

	filters := []string{"run.yipyap.alert.*", "run.yipyap.monitor.*"}
	_, _ = doPollFilters(t, h, h.apiKey, filters, "", 0)

	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.monitor.up")
	publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.incident.opened")

	time.Sleep(100 * time.Millisecond)

	resp, body := doPollFilters(t, h, h.apiKey, filters, "", 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(body.Events) != 2 {
		t.Fatalf("want 2 events matching either glob, got %d", len(body.Events))
	}
	for _, raw := range body.Events {
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if !strings.HasPrefix(env.Type, "run.yipyap.alert.") &&
			!strings.HasPrefix(env.Type, "run.yipyap.monitor.") {
			t.Fatalf("non-matching event slipped through filters: %s", env.Type)
		}
	}
}

// TestPoll_MaxRespected: 10 events; max=3 returns at most 3.
func TestPoll_MaxRespected(t *testing.T) {
	h := setupPollTest(t)

	_, _ = doPoll(t, h, h.apiKey, "", "", 3)

	for i := 0; i < 10; i++ {
		publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.firing")
	}
	time.Sleep(100 * time.Millisecond)

	resp, body := doPoll(t, h, h.apiKey, "", "", 3)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(body.Events) > 3 {
		t.Fatalf("want at most 3 events, got %d", len(body.Events))
	}
}

// TestPoll_EmptyResponseWhenNothingPending: idle bus → 200 + events:[].
func TestPoll_EmptyResponseWhenNothingPending(t *testing.T) {
	h := setupPollTest(t)

	start := time.Now()
	resp, body := doPoll(t, h, h.apiKey, "", "", 0)
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if body.Events == nil {
		t.Fatalf("events must be present (possibly empty), not null")
	}
	if len(body.Events) != 0 {
		t.Fatalf("want 0 events for idle bus, got %d", len(body.Events))
	}
	// The drain window is bounded; make sure the handler returned in a
	// reasonable time (not e.g. blocked on the poll interval forever).
	if elapsed > 5*time.Second {
		t.Fatalf("poll took too long on idle bus: %v", elapsed)
	}
}

// TestPoll_CrossOrgIsolation: events for org-2 don't appear in org-1 poll.
func TestPoll_CrossOrgIsolation(t *testing.T) {
	h := setupPollTest(t)

	// Prime both orgs' consumers so subsequent publishes are retained.
	_, _ = doPoll(t, h, h.apiKey, "", "", 0)
	_, _ = doPoll(t, h, h.otherKey, "", "", 0)

	publishTestEvent(t, h.msgBus, h.otherOrg.ID, "run.yipyap.alert.firing")
	publishTestEvent(t, h.msgBus, h.otherOrg.ID, "run.yipyap.alert.firing")
	time.Sleep(100 * time.Millisecond)

	resp, body := doPoll(t, h, h.apiKey, "", "", 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(body.Events) != 0 {
		t.Fatalf("cross-org bleed: got %d events on org-1 poll", len(body.Events))
	}

	// Sanity-check the other org does see its own events.
	resp2, body2 := doPoll(t, h, h.otherKey, "", "", 0)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp2.StatusCode)
	}
	if len(body2.Events) != 2 {
		t.Fatalf("other org should see its 2 events, got %d", len(body2.Events))
	}
}

// TestPoll_Unauthorized: no bearer → 401.
func TestPoll_Unauthorized(t *testing.T) {
	h := setupPollTest(t)

	resp, _ := doPoll(t, h, "", "", "", 0)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}
