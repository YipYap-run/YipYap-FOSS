package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
)

type replicaPollResult struct {
	ids    []string
	cursor string
}

func pollReplica(t *testing.T, baseURL, bearer, sourceID, since string) replicaPollResult {
	t.Helper()
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/cloudevents/poll?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-YipYap-Source-ID", sourceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("poll status %d: %s", resp.StatusCode, raw)
	}
	var body struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := replicaPollResult{cursor: body.NextCursor}
	for _, e := range body.Events {
		out.ids = append(out.ids, e.ID)
	}
	return out
}

func ackReplica(t *testing.T, baseURL, bearer, sourceID, cursor string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"cursor": cursor})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/cloudevents/ack", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-YipYap-Source-ID", sourceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestCEFeed_PositionSurvivesReplicaSwitch(t *testing.T) {
	h := setupPollTest(t)
	second := httptest.NewServer(api.NewServer(h.store, h.jwt, h.msgBus, nil, nil, "", "", api.ServerOptions{
		APIKeyHasher: h.hasher,
	}))
	t.Cleanup(second.Close)

	const src = "shared-source"
	c0 := pollReplica(t, h.ts.URL, h.apiKey, src, "").cursor
	if c0 == "" {
		t.Fatal("replica A: priming poll returned no cursor")
	}
	if got := pollReplica(t, second.URL, h.apiKey, src, ""); got.cursor == "" {
		t.Fatal("replica B: priming poll returned no cursor")
	}

	ev := publishTestEvent(t, h.msgBus, h.org.ID, "run.yipyap.alert.fired.v1")
	time.Sleep(100 * time.Millisecond)

	got := pollReplica(t, h.ts.URL, h.apiKey, src, c0)
	if len(got.ids) != 1 || got.ids[0] != ev.ID() {
		t.Fatalf("replica A read %v, want [%s]", got.ids, ev.ID())
	}
	if code := ackReplica(t, h.ts.URL, h.apiKey, src, got.cursor); code != http.StatusNoContent {
		t.Fatalf("ack on replica A: status %d", code)
	}

	if again := pollReplica(t, second.URL, h.apiKey, src, ""); len(again.ids) != 0 {
		t.Fatalf("replica B replayed %v after the ack on A", again.ids)
	}

	if code := ackReplica(t, second.URL, h.apiKey, src, c0); code != http.StatusNoContent {
		t.Fatalf("ack on replica B: status %d", code)
	}
	if replayed := pollReplica(t, h.ts.URL, h.apiKey, src, ""); len(replayed.ids) != 1 || replayed.ids[0] != ev.ID() {
		t.Fatalf("replica A read %v after the position moved back, want [%s]", replayed.ids, ev.ID())
	}
}
