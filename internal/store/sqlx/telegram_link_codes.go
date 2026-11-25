package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// TelegramLinkCodeStore implements store.TelegramLinkCodeStore.
type TelegramLinkCodeStore struct {
	q Queryable
	d Dialect
}

// NewTelegramLinkCodeStore returns a TelegramLinkCodeStore bound to q.
func NewTelegramLinkCodeStore(q Queryable, d Dialect) *TelegramLinkCodeStore {
	return &TelegramLinkCodeStore{q: q, d: d}
}

const telegramLinkCodeCols = `id, code_hash, org_id, created_by, purpose, subject_id, channel_name,
	expires_at, used_at, linked_chat_id, linked_chat_title, linked_subject_id, created_at`

func (s *TelegramLinkCodeStore) Create(ctx context.Context, c *domain.TelegramLinkCode) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO telegram_link_codes (`+telegramLinkCodeCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.CodeHash, c.OrgID, c.CreatedBy, c.Purpose, NullString(c.SubjectID), c.ChannelName,
		s.d.TimeArg(c.ExpiresAt), s.d.OptionalTimeArg(c.UsedAt),
		NullString(c.LinkedChatID), NullString(c.LinkedChatTitle), NullString(c.LinkedSubjectID),
		s.d.TimeArg(c.CreatedAt))
	return err
}

func (s *TelegramLinkCodeStore) scan(row Scanner) (*domain.TelegramLinkCode, error) {
	var c domain.TelegramLinkCode
	var subjectID, chatID, chatTitle, linkedSubject sql.NullString
	var expiresAt, usedAt, createdAt Time
	err := row.Scan(&c.ID, &c.CodeHash, &c.OrgID, &c.CreatedBy, &c.Purpose, &subjectID, &c.ChannelName,
		&expiresAt, &usedAt, &chatID, &chatTitle, &linkedSubject, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.SubjectID = subjectID.String
	c.LinkedChatID = chatID.String
	c.LinkedChatTitle = chatTitle.String
	c.LinkedSubjectID = linkedSubject.String
	c.ExpiresAt = expiresAt.T
	c.UsedAt = usedAt.TimePtr()
	c.CreatedAt = createdAt.T
	return &c, nil
}

func (s *TelegramLinkCodeStore) GetByID(ctx context.Context, id string) (*domain.TelegramLinkCode, error) {
	return s.scan(s.q.QueryRowContext(ctx,
		`SELECT `+telegramLinkCodeCols+` FROM telegram_link_codes WHERE id = ?`, id))
}

func (s *TelegramLinkCodeStore) GetByHash(ctx context.Context, codeHash string) (*domain.TelegramLinkCode, error) {
	return s.scan(s.q.QueryRowContext(ctx,
		`SELECT `+telegramLinkCodeCols+` FROM telegram_link_codes WHERE code_hash = ?`, codeHash))
}

func (s *TelegramLinkCodeStore) Claim(ctx context.Context, id string, now time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE telegram_link_codes SET used_at = ?
		  WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		s.d.TimeArg(now), id, s.d.TimeArg(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *TelegramLinkCodeStore) SetLinked(ctx context.Context, id, chatID, chatTitle, subjectID string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE telegram_link_codes SET linked_chat_id = ?, linked_chat_title = ?, linked_subject_id = ?
		  WHERE id = ?`,
		chatID, chatTitle, subjectID, id)
	return err
}

func (s *TelegramLinkCodeStore) CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM telegram_link_codes WHERE org_id = ? AND created_at >= ?`,
		orgID, s.d.TimeArg(since)).Scan(&n)
	return n, err
}
