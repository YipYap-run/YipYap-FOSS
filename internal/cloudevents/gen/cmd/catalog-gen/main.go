// Command catalog-gen renders Go types, JSON Schemas, and TypeScript
// types from the YAML event catalog in internal/cloudevents/catalog.
//
// It is invoked via `go generate ./internal/cloudevents/...`, which runs
// the directive from the directory containing the file that declares it
// (internal/cloudevents/). To keep the default flag values stable
// regardless of where the binary is launched from, main computes the
// repo root from runtime.Caller(0), the source file lives at a known
// depth below the module root, and resolves default paths against it.
// Flag overrides are honored verbatim (no root-relative rewriting) so
// callers can point the driver at arbitrary locations for testing.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/gen"
)

// repoRoot walks up from this source file to the module root.
// This file lives at <root>/internal/cloudevents/gen/cmd/catalog-gen/main.go,
// i.e. five directories above the repo root.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", ".."))
}

func main() {
	root := repoRoot()
	dir := flag.String("catalog", filepath.Join(root, "internal/cloudevents/catalog"), "catalog dir")
	goOut := flag.String("go-out", filepath.Join(root, "internal/cloudevents/types/types.gen.go"), "go output path")
	jsonOut := flag.String("json-out", filepath.Join(root, "internal/cloudevents/schema"), "json schema output dir")
	tsOut := flag.String("ts-out", filepath.Join(root, "web/src/lib/cloudevents/types.gen.ts"), "ts output path")
	flag.Parse()

	cat, err := gen.ParseCatalog(*dir)
	if err != nil {
		log.Fatal(err)
	}
	if err := writeFile(*goOut, mustRender(gen.RenderGoTypes(cat, "types"))); err != nil {
		log.Fatal(err)
	}
	files, err := gen.RenderJSONSchemas(cat)
	if err != nil {
		log.Fatal(err)
	}
	for name, b := range files {
		if err := writeFile(filepath.Join(*jsonOut, name), b); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeFile(*tsOut, mustRender(gen.RenderTSTypes(cat))); err != nil {
		log.Fatal(err)
	}
}

func mustRender(b []byte, err error) []byte {
	if err != nil {
		log.Fatal(err)
	}
	return b
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
