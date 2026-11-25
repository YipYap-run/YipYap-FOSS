package sqlite

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlx"
)

var _ store.ContactMethodProvider = (*SQLiteStore)(nil)

// ContactMethods returns the per-user contact method store.
func (s *SQLiteStore) ContactMethods() store.ContactMethodStore {
	return sqlx.NewContactMethodStore(s.q, dialect)
}

// NotificationRules returns the per-user notification rule store.
func (s *SQLiteStore) NotificationRules() store.NotificationRuleStore {
	return sqlx.NewNotificationRuleStore(s.q, dialect)
}
