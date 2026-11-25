package sqlx

import "context"

// UsageStore is a no-op stub in FOSS builds, which do not meter SMS/voice.
type UsageStore struct{}

// NewUsageStore returns a no-op UsageStore for FOSS builds.
func NewUsageStore(_ Queryable, _ Dialect) *UsageStore { return &UsageStore{} }

func (s *UsageStore) Reserve(_ context.Context, _, _, _ string, _ int) (int, bool, error) {
	return 0, true, nil
}

func (s *UsageStore) Get(_ context.Context, _, _ string) (int, int, error) { return 0, 0, nil }

func (s *UsageStore) ClaimWarning(_ context.Context, _, _, _ string, _ int) (bool, error) {
	return false, nil
}
