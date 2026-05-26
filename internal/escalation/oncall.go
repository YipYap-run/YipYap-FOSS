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
func (r *OnCallResolver) Resolve(_ context.Context, teamID string, _ int, _ time.Time) (string, error) {
	return "", fmt.Errorf("on-call resolution requires Pro tier (team %s)", teamID)
}
