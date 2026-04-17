package main

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
)

func openBus(_ *config.Config) (bus.Bus, error) {
	return bus.NewChannel(), nil
}
