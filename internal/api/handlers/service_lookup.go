package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// lookupServiceName is a no-op in FOSS builds (no service catalog).
func lookupServiceName(_ context.Context, _ store.Store, _ string) string {
	return ""
}

// serviceExistsInOrg fail-opens in FOSS, the service catalog isn't
// compiled in, so the field is unverifiable. Callers that store a
// service_id will simply see an empty service name on read (handled
// by lookupServiceName above).
func serviceExistsInOrg(_ context.Context, _ store.Store, _, _ string) bool {
	return true
}
