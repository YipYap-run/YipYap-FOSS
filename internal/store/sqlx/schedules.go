package sqlx

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ScheduleStore is a no-op stub in FOSS builds.
type ScheduleStore struct{}

// NewScheduleStore returns a no-op ScheduleStore for FOSS builds.
func NewScheduleStore(_ Queryable, _ Dialect) *ScheduleStore { return &ScheduleStore{} }

func (s *ScheduleStore) Create(_ context.Context, _ *domain.Schedule) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) GetByID(_ context.Context, _ string) (*domain.Schedule, error) {
	return nil, fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) ListByTeam(_ context.Context, _ string) ([]*domain.Schedule, error) {
	return nil, nil
}

func (s *ScheduleStore) ListByOrg(_ context.Context, _ string) ([]*domain.Schedule, error) {
	return nil, nil
}

func (s *ScheduleStore) Update(_ context.Context, _ *domain.Schedule) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) CreateOverride(_ context.Context, _ *domain.ScheduleOverride) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) GetOverrideByID(_ context.Context, _ string) (*domain.ScheduleOverride, error) {
	return nil, fmt.Errorf("schedules require Pro tier")
}

func (s *ScheduleStore) ListOverrides(_ context.Context, _ string) ([]*domain.ScheduleOverride, error) {
	return nil, nil
}

func (s *ScheduleStore) DeleteOverride(_ context.Context, _ string) error {
	return fmt.Errorf("schedules require Pro tier")
}
