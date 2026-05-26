package store

import "context"

// OrgSettingsStore manages per-org key-value settings.
type OrgSettingsStore interface {
	Get(ctx context.Context, orgID, key string) (string, error)
	Set(ctx context.Context, orgID, key, value string) error
	Delete(ctx context.Context, orgID, key string) error
	GetAll(ctx context.Context, orgID string) (map[string]string, error)
}

// OrgSettingsProvider is implemented by stores that support org settings.
type OrgSettingsProvider interface {
	OrgSettings() OrgSettingsStore
}
