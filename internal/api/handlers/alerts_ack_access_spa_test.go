package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The SPA decides whether to show Acknowledge to a viewer from the same list
// of timeline events this package uses (web/src/state/permissions.mjs). If
// the two drift, viewers either see a button that returns 403 or lose one
// they are allowed to use.
func TestPagedEventTypesMatchSPA(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "state", "permissions.mjs"))
	if err != nil {
		t.Fatalf("read SPA permissions module: %v", err)
	}
	m := regexp.MustCompile(`PAGED_EVENT_TYPES = new Set\(\[([^\]]*)\]\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("PAGED_EVENT_TYPES not found in permissions.mjs")
	}
	var spa []string
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1) {
		spa = append(spa, string(q[1]))
	}
	var server []string
	for k, ok := range pagedEventTypes {
		if ok {
			server = append(server, string(k))
		}
	}
	sort.Strings(spa)
	sort.Strings(server)
	if strings.Join(spa, ",") != strings.Join(server, ",") {
		t.Errorf("SPA paged events %v, server %v", spa, server)
	}
}
