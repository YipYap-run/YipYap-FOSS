package domain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEmbeddedTZData proves zone lookups work with no system zoneinfo. The
// check runs in a child process because the time package caches ZONEINFO
// on first use.
func TestEmbeddedTZData(t *testing.T) {
	if os.Getenv("YIPYAP_TZDATA_CHILD") == "1" {
		for _, tz := range []string{"America/New_York", "Europe/Berlin", "Asia/Kolkata"} {
			if _, err := time.LoadLocation(tz); err != nil {
				t.Fatalf("LoadLocation(%q) with empty ZONEINFO: %v", tz, err)
			}
			if err := ValidateTimezone(tz); err != nil {
				t.Fatalf("ValidateTimezone(%q): %v", tz, err)
			}
		}
		return
	}
	empty := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEmbeddedTZData$", "-test.v")
	// Point ZONEINFO at an empty dir and hide GOROOT so the only zone
	// source left is the embedded copy.
	cmd.Env = append(os.Environ(),
		"YIPYAP_TZDATA_CHILD=1",
		"ZONEINFO="+empty,
		"GOROOT="+empty,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	// Guard against a -test.run typo that makes the child run nothing and
	// exit 0.
	if !strings.Contains(string(out), "--- PASS: TestEmbeddedTZData") {
		t.Fatalf("child did not run the zone checks:\n%s", out)
	}
}

func TestValidateTimezone(t *testing.T) {
	for _, tz := range []string{"", "UTC", "America/New_York"} {
		if err := ValidateTimezone(tz); err != nil {
			t.Errorf("ValidateTimezone(%q) = %v, want nil", tz, err)
		}
	}
	for _, tz := range []string{"Local", "Mars/Olympus", "EST5EDT/../../etc", "not a zone"} {
		if err := ValidateTimezone(tz); err == nil {
			t.Errorf("ValidateTimezone(%q) = nil, want error", tz)
		}
	}
}

// TestBinariesEmbedTZData checks that every shipped binary links
// time/tzdata in both editions. The ZONEINFO test above cannot catch a
// missing embed on hosts that ship /usr/share/zoneinfo, but release images
// are FROM scratch and have none.
func TestBinariesEmbedTZData(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	cmds, err := filepath.Glob("../../cmd/*")
	if err != nil || len(cmds) == 0 {
		t.Fatalf("no cmd dirs found: %v", err)
	}
	for _, tags := range []string{"", "foss"} {
		for _, dir := range cmds {
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			// The split binaries are SaaS-only and deleted from the FOSS repo.
			if tags == "foss" && filepath.Base(dir) != "yipyap" {
				continue
			}
			args := []string{"list", "-deps"}
			if tags != "" {
				args = append(args, "-tags", tags)
			}
			args = append(args, "./"+dir)
			out, err := exec.Command(goBin, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
			}
			found := false
			for _, line := range strings.Split(string(out), "\n") {
				if strings.TrimSpace(line) == "time/tzdata" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s (tags=%q) does not link time/tzdata", dir, tags)
			}
		}
	}
}
