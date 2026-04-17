package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// lookupServiceName is a no-op in FOSS builds (no service catalog).
func lookupServiceName(_ context.Context, _ store.Store, _ string) string {
	return ""
}
