package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

const gapTestOrg = "org-gap"

var gapTestSubject = cepub.FanoutSubjectFor(gapTestOrg, "run.yipyap.alert.fired.v1")

func gapTestEventData(id string) []byte {
	return []byte(fmt.Sprintf(`{"specversion":"1.0","id":%q,"type":"run.yipyap.alert.fired.v1","data":{}}`, id))
}

func newTrimmedLog(t *testing.T) (*bus.MemoryEventLog, bus.LogState) {
	t.Helper()
	channel := bus.NewChannel()
	t.Cleanup(func() { _ = channel.Close() })
	log, err := bus.NewMemoryEventLog(channel, cepub.FanoutSubjectFor("*", ">"), 2, time.Hour)
	if err != nil {
		t.Fatalf("NewMemoryEventLog: %v", err)
	}
	for i := 0; i < 3; i++ {
		log.Append(gapTestSubject, gapTestEventData(fmt.Sprintf("evt-%d", i)))
	}
	st, err := log.LogState(context.Background())
	if err != nil {
		t.Fatalf("LogState: %v", err)
	}
	if st.FirstSeq != 2 || st.LastSeq != 3 {
		t.Fatalf("log bounds = %+v, want first 2 last 3", st)
	}
	return log, st
}

func gapTestRequest(t *testing.T, since string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/poll", nil)
	q := r.URL.Query()
	q.Set("since", since)
	r.URL.RawQuery = q.Encode()
	r.Header.Set(CESourceIDHeader, "gap-test")
	return r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: gapTestOrg}))
}

type gapPollBody struct {
	Events []struct {
		ID string `json:"id"`
	} `json:"events"`
	NextCursor string `json:"next_cursor"`
	Gap        *ceGap `json:"gap"`
}

func TestCEPoll_ReportsGapWhenCursorPredatesRetention(t *testing.T) {
	log, st := newTrimmedLog(t)
	feed := newCloudEventsFeedWithLog(log, nil)
	h := NewCloudEventsPollHandlerWithFeed(feed)
	h.drainWindow = 200 * time.Millisecond

	w := httptest.NewRecorder()
	h.Poll(w, gapTestRequest(t, encodeCECursor(st.Epoch, 0)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body gapPollBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Gap == nil {
		t.Fatal("cursor older than FirstSeq did not report a gap")
	}
	if body.Gap.After != 0 || body.Gap.FirstAvailable != st.FirstSeq {
		t.Fatalf("gap = %+v, want after 0 first %d", body.Gap, st.FirstSeq)
	}
	if len(body.Events) != 2 {
		t.Fatalf("delivered %d retained events, want 2", len(body.Events))
	}
}

func TestCEPoll_NoGapForRetainedCursor(t *testing.T) {
	log, st := newTrimmedLog(t)
	feed := newCloudEventsFeedWithLog(log, nil)
	h := NewCloudEventsPollHandlerWithFeed(feed)
	h.drainWindow = 200 * time.Millisecond

	w := httptest.NewRecorder()
	h.Poll(w, gapTestRequest(t, encodeCECursor(st.Epoch, st.FirstSeq)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body gapPollBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Gap != nil {
		t.Fatalf("retained cursor reported gap %+v", body.Gap)
	}
	if len(body.Events) != 1 || body.Events[0].ID != "evt-2" {
		t.Fatalf("delivered %v, want [evt-2]", body.Events)
	}
}

func TestCEStream_ReportsGapInHeaders(t *testing.T) {
	log, st := newTrimmedLog(t)
	feed := newCloudEventsFeedWithLog(log, nil)
	h := NewCloudEventsStreamHandlerWithFeed(feed)

	streamHeaders := func(since string) http.Header {
		r := gapTestRequest(t, since)
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		w := httptest.NewRecorder()
		h.Stream(w, r.WithContext(ctx))
		return w.Header()
	}

	headers := streamHeaders(encodeCECursor(st.Epoch, 0))
	if headers.Get("X-YipYap-Log-Gap") != "true" {
		t.Fatalf("gap header = %q, want true", headers.Get("X-YipYap-Log-Gap"))
	}
	if got := headers.Get("X-YipYap-Log-First-Seq"); got != fmt.Sprint(st.FirstSeq) {
		t.Fatalf("first-seq header = %q, want %d", got, st.FirstSeq)
	}

	headers = streamHeaders(encodeCECursor(st.Epoch, st.FirstSeq))
	if headers.Get("X-YipYap-Log-Gap") != "" {
		t.Fatalf("retained cursor sent gap header %q", headers.Get("X-YipYap-Log-Gap"))
	}
}

func TestGapAfter(t *testing.T) {
	tests := []struct {
		name string
		pos  uint64
		st   bus.LogState
		want bool
	}{
		{"empty log", 0, bus.LogState{FirstSeq: 0, LastSeq: 0}, false},
		{"cursor at first retained", 1, bus.LogState{FirstSeq: 2, LastSeq: 9}, false},
		{"cursor before first retained", 0, bus.LogState{FirstSeq: 2, LastSeq: 9}, true},
		{"nothing trimmed", 4, bus.LogState{FirstSeq: 1, LastSeq: 9}, false},
		{"everything trimmed", 5, bus.LogState{FirstSeq: 10, LastSeq: 9}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gapAfter(ceCursor{S: tt.pos}, tt.st)
			if (got != nil) != tt.want {
				t.Fatalf("gapAfter = %+v, want gap %v", got, tt.want)
			}
		})
	}
}
