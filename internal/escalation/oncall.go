package escalation

import (
	"context"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// OnCallResolver is a no-op stub in FOSS builds (on-call schedules require Pro).
type OnCallResolver struct{}

// NewOnCallResolver returns a stub resolver.
func NewOnCallResolver(_ store.Store) *OnCallResolver {
	return &OnCallResolver{}
}

// Resolve always returns an error in FOSS builds.
func (r *OnCallResolver) Resolve(_ context.Context, _, scheduleID string, _ int, _ time.Time) (string, error) {
	return "", fmt.Errorf("on-call schedules are not available in this edition (schedule %s)", scheduleID)
}
