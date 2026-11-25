package handlers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
)

type scriptedEventLog struct {
	mu      sync.Mutex
	states  []bus.LogState
	batches [][]bus.LogEntry
}

func (l *scriptedEventLog) LogState(context.Context) (bus.LogState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.states) > 1 {
		st := l.states[0]
		l.states = l.states[1:]
		return st, nil
	}
	if len(l.states) == 1 {
		return l.states[0], nil
	}
	return bus.LogState{}, nil
}

func (l *scriptedEventLog) ReadLog(ctx context.Context, _ string, _ uint64, _ int, _ time.Duration) ([]bus.LogEntry, error) {
	l.mu.Lock()
	if len(l.batches) > 0 {
		batch := l.batches[0]
		l.batches = l.batches[1:]
		l.mu.Unlock()
		return batch, nil
	}
	l.mu.Unlock()
	<-ctx.Done()
	return nil, nil
}

func scriptedStreamRequest(t *testing.T, since string) *http.Request {
	t.Helper()
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/stream?"+q.Encode(), nil)
	r.Header.Set(CESourceIDHeader, "stream-gap-test")
	return r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: gapTestOrg}))
}

func TestCEStream_BusyGapClosesBeforeDelivering(t *testing.T) {
	log := &scriptedEventLog{
		states: []bus.LogState{
			{Epoch: "e", FirstSeq: 1, LastSeq: 4},
			{Epoch: "e", FirstSeq: 10, LastSeq: 11},
		},
		batches: [][]bus.LogEntry{{
			{Seq: 10, Subject: gapTestSubject, Data: gapTestEventData("evt-10")},
			{Seq: 11, Subject: gapTestSubject, Data: gapTestEventData("evt-11")},
		}},
	}
	feed := newCloudEventsFeedWithLog(log, nil)
	h := NewCloudEventsStreamHandlerWithFeed(feed)

	w := httptest.NewRecorder()
	r := scriptedStreamRequest(t, encodeCECursor("e", 0))
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	h.Stream(w, r.WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("delivered %q before closing on the gap", w.Body.String())
	}
}

func TestCEStream_InitialGapStillDeliversRetainedEvents(t *testing.T) {
	log := &scriptedEventLog{
		states: []bus.LogState{{Epoch: "e", FirstSeq: 5, LastSeq: 6}},
		batches: [][]bus.LogEntry{{
			{Seq: 5, Subject: gapTestSubject, Data: gapTestEventData("evt-5")},
			{Seq: 6, Subject: gapTestSubject, Data: gapTestEventData("evt-6")},
		}},
	}
	feed := newCloudEventsFeedWithLog(log, nil)
	h := NewCloudEventsStreamHandlerWithFeed(feed)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Stream(w, r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: gapTestOrg})))
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := url.Values{"since": {encodeCECursor("e", 0)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(CESourceIDHeader, "stream-gap-test")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("X-YipYap-Log-Gap") != "true" {
		t.Fatalf("gap header = %q, want true", resp.Header.Get("X-YipYap-Log-Gap"))
	}

	lines := make(chan string, 2)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				lines <- line
			}
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-lines:
		case <-time.After(3 * time.Second):
			t.Fatalf("got %d of 2 retained events before the stream stalled", i)
		}
	}
}
