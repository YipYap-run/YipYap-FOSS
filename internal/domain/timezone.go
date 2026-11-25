package domain

import (
	"fmt"
	"time"

	// Embed the IANA zone database. Release images are FROM scratch and
	// built with -trimpath, so neither /usr/share/zoneinfo nor the GOROOT
	// zoneinfo.zip exists at runtime. Every binary imports domain, so this
	// covers the monolith, the split binaries and the FOSS build. (core-3)
	_ "time/tzdata"
)

// ValidateTimezone reports whether tz is a usable IANA zone name. An empty
// string is accepted and means UTC. "Local" is rejected because it depends
// on the host the process runs on.
func ValidateTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	if tz == "Local" {
		return fmt.Errorf("invalid timezone %q: use an IANA zone name such as America/New_York", tz)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: use an IANA zone name such as America/New_York", tz)
	}
	return nil
}
