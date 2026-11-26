package main

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

// startOrgOTEL is a no-op in FOSS builds, which have no per-org export.
func startOrgOTEL(_ store.Store, _ *crypto.Envelope) (telemetry.OrgRecorder, func(context.Context, string) error, func()) {
	return nil, nil, func() {}
}
