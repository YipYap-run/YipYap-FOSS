package sqlite

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

func TestMigrationVersionsAreUnique(t *testing.T) {
	seen := map[int]string{}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Fatalf("%s: no NNN_ prefix", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("%s: bad version: %v", name, err)
		}
		if prev, dup := seen[v]; dup {
			t.Fatalf("version %d used by %s and %s", v, prev, name)
		}
		seen[v] = name
	}
}
