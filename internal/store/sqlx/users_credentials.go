package sqlx

import "context"

// clearCredentialRows is a no-op in FOSS builds, which store no second
// factors for a user.
func (s *UserStore) clearCredentialRows(_ context.Context, _ string) error { return nil }
