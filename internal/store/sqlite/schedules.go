package sqlite

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type scheduleStore struct{ q queryable }

func (s *scheduleStore) Create(_ context.Context, _ *domain.Schedule) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) GetByID(_ context.Context, _ string) (*domain.Schedule, error) {
	return nil, fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) ListByTeam(_ context.Context, _ string) ([]*domain.Schedule, error) {
	return nil, nil
}

func (s *scheduleStore) ListByOrg(_ context.Context, _ string) ([]*domain.Schedule, error) {
	return nil, nil
}

func (s *scheduleStore) Update(_ context.Context, _ *domain.Schedule) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) CreateOverride(_ context.Context, _ *domain.ScheduleOverride) error {
	return fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) GetOverrideByID(_ context.Context, _ string) (*domain.ScheduleOverride, error) {
	return nil, fmt.Errorf("schedules require Pro tier")
}

func (s *scheduleStore) ListOverrides(_ context.Context, _ string) ([]*domain.ScheduleOverride, error) {
	return nil, nil
}

func (s *scheduleStore) DeleteOverride(_ context.Context, _ string) error {
	return fmt.Errorf("schedules require Pro tier")
}
