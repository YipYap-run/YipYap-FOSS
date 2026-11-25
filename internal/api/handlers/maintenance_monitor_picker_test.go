package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
)

// The maintenance settings picker (fetchAllMonitors in
// web/src/pages/settings/maintenance.jsx) walks GET /monitors with
// limit=100&offset=N and stops at the first short page. This pins that
// contract: every monitor shows up exactly once across pages, including
// orgs with more than the 50-row default page.
func TestMonitorList_PagesForMaintenancePicker(t *testing.T) {
	s, org, user := setupMaintenanceTest(t)
	h := handlers.NewMonitorHandler(s)

	const total = 130
	want := map[string]bool{}
	for i := 0; i < total; i++ {
		want[createMaintenanceMonitor(t, s, org.ID).ID] = true
	}

	seen := map[string]int{}
	pages := 0
	for offset := 0; ; offset += 100 {
		path := fmt.Sprintf("/monitors?limit=100&offset=%d", offset)
		w, _, ctx := requestWithClaims(t, "GET", path, "", user)
		h.List(w, httptest.NewRequest("GET", path, nil).WithContext(ctx))
		if w.Code != 200 {
			t.Fatalf("offset %d: status %d body=%s", offset, w.Code, w.Body.String())
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		pages++
		for _, r := range rows {
			seen[r.ID]++
		}
		if len(rows) < 100 {
			break
		}
		if pages > 5 {
			t.Fatal("paging did not stop")
		}
	}
	if pages != 2 {
		t.Fatalf("pages = %d, want 2", pages)
	}
	if len(seen) != total {
		t.Fatalf("saw %d monitors, want %d", len(seen), total)
	}
	for id, n := range seen {
		if !want[id] || n != 1 {
			t.Fatalf("monitor %s seen %d times (known=%v)", id, n, want[id])
		}
	}
}
