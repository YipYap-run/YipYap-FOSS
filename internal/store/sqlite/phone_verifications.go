package sqlite

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlx"
)

// PhoneVerifications implements store.PhoneVerificationProvider.
func (s *SQLiteStore) PhoneVerifications() store.PhoneVerificationStore {
	return sqlx.NewPhoneVerificationStore(s.q, dialect)
}
