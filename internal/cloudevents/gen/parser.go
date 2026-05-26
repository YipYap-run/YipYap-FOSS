package gen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ParseCatalog reads all *.yaml files in dir and returns a validated Catalog.
func ParseCatalog(dir string) (*Catalog, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no *.yaml files in %s", dir)
	}
	cat := &Catalog{}
	seen := map[string]string{} // type -> originating file path
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", m, err)
		}
		var ev Event
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&ev); err != nil {
			return nil, fmt.Errorf("parse %s: %w", m, err)
		}
		if ev.Type == "" {
			return nil, fmt.Errorf("%s: type is required", m)
		}
		if ev.Direction == "" {
			return nil, fmt.Errorf("%s: direction is required", m)
		}
		if _, ok := validDirections[ev.Direction]; !ok {
			return nil, fmt.Errorf("%s: unknown direction %q", m, ev.Direction)
		}
		if prev, ok := seen[ev.Type]; ok {
			return nil, fmt.Errorf("duplicate type %q defined in %s and %s", ev.Type, prev, m)
		}
		seen[ev.Type] = m
		cat.Events = append(cat.Events, ev)
	}
	sort.Slice(cat.Events, func(i, j int) bool { return cat.Events[i].Type < cat.Events[j].Type })
	return cat, nil
}
