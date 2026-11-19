package wiring

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
)

// OpenBus returns the in-process channel bus (the only one in FOSS builds).
func OpenBus(_ *config.Config) (bus.Bus, error) {
	return bus.NewChannel(), nil
}
