package wiring

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// InitBilling is a no-op in FOSS builds, which ship no billing integration.
func InitBilling(_ *config.Config) (interface{}, string) {
	return nil, ""
}

// StartBilling is a no-op in FOSS builds.
func StartBilling(_ context.Context, _ interface{}, _ store.Store) {}
