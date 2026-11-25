package sqlite

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlx"
)

// TelegramLinkCodes implements store.TelegramLinkCodeProvider.
func (s *SQLiteStore) TelegramLinkCodes() store.TelegramLinkCodeStore {
	return sqlx.NewTelegramLinkCodeStore(s.q, dialect)
}
