package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// monitorLookupStore answers Monitors().GetByID with a fixed result. Every
// other store method panics through the nil embedded interfaces.
type monitorLookupStore struct {
	store.Store
	monitors monitorLookup
}

func (s monitorLookupStore) Monitors() store.MonitorStore { return s.monitors }

func (s monitorLookupStore) CloudEventPositions() store.CloudEventPositionStore { return nil }

type monitorLookup struct {
	store.MonitorStore
	m   *domain.Monitor
	err error
}

func (l monitorLookup) GetByID(context.Context, string) (*domain.Monitor, error) {
	return l.m, l.err
}

// A store failure while checking monitor_id must not look like a bad
// filter: a 400 would tell the adapter to stop retrying.
func TestCEFeed_MonitorLookupErrors(t *testing.T) {
	cases := []struct {
		name string
		look monitorLookup
		want int
	}{
		{"store down", monitorLookup{err: errors.New("connection refused")}, http.StatusInternalServerError},
		{"not found", monitorLookup{err: fmt.Errorf("monitor not found")}, http.StatusBadRequest},
		{"other org", monitorLookup{m: &domain.Monitor{ID: "m1", OrgID: "org-2"}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		feed := newCloudEventsFeedWithLog(unavailableLog{}, monitorLookupStore{monitors: tc.look})
		r := httptest.NewRequest(http.MethodGet, "/api/v1/cloudevents/poll?monitor_id=m1", nil)
		r = r.WithContext(middleware.SetClaims(r.Context(), &auth.Claims{OrgID: "org-1"}))
		w := httptest.NewRecorder()
		if req := feed.parseRequest(w, r); req != nil {
			t.Fatalf("%s: request accepted", tc.name)
		}
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, w.Code, tc.want)
		}
	}
}
