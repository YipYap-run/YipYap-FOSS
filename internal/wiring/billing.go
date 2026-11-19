package wiring

import "github.com/YipYap-run/YipYap-FOSS/internal/config"

// InitBilling is a no-op in FOSS builds, which ship no billing integration.
func InitBilling(_ *config.Config) (interface{}, string) {
	return nil, ""
}
