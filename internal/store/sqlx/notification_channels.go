package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// NotificationChannelStore implements store.NotificationChannelStore across
// SQL backends.
type NotificationChannelStore struct {
	q Queryable
	d Dialect
}

// NewNotificationChannelStore returns a NotificationChannelStore bound to q.
func NewNotificationChannelStore(q Queryable, d Dialect) *NotificationChannelStore {
	return &NotificationChannelStore{q: q, d: d}
}

func (s *NotificationChannelStore) Create(ctx context.Context, ch *domain.NotificationChannel) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO notification_channels (id, org_id, type, name, config, enabled, verified_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.OrgID, ch.Type, ch.Name, ch.Config, s.d.BoolArg(ch.Enabled), s.d.OptionalTimeArg(ch.VerifiedAt))
	return err
}

func (s *NotificationChannelStore) GetByID(ctx context.Context, id string) (*domain.NotificationChannel, error) {
	ch, err := scanChannel(s.q.QueryRowContext(ctx,
		`SELECT `+channelColumns+` FROM notification_channels WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("notification channel not found")
		}
		return nil, err
	}
	return ch, nil
}

const channelColumns = `id, org_id, type, name, config, enabled, verified_at,
	last_success_at, last_failure_at, last_error, consecutive_failures,
	failure_notified_at`

func scanChannel(sc Scanner) (*domain.NotificationChannel, error) {
	var ch domain.NotificationChannel
	var enabled Bool
	var verifiedAt, lastOK, lastFail, notified Time
	if err := sc.Scan(&ch.ID, &ch.OrgID, &ch.Type, &ch.Name, &ch.Config, &enabled, &verifiedAt,
		&lastOK, &lastFail, &ch.LastError, &ch.ConsecutiveFailures, &notified); err != nil {
		return nil, err
	}
	ch.Enabled = bool(enabled)
	ch.VerifiedAt = verifiedAt.TimePtr()
	ch.LastSuccessAt = lastOK.TimePtr()
	ch.LastFailureAt = lastFail.TimePtr()
	ch.FailureNotifiedAt = notified.TimePtr()
	return &ch, nil
}

func (s *NotificationChannelStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.NotificationChannel, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+channelColumns+` FROM notification_channels
		 WHERE org_id = ? ORDER BY name LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.NotificationChannel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (s *NotificationChannelStore) Update(ctx context.Context, ch *domain.NotificationChannel) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET type = ?, name = ?, config = ?, enabled = ? WHERE id = ?`,
		ch.Type, ch.Name, ch.Config, s.d.BoolArg(ch.Enabled), ch.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// SetVerifiedAt records (or, with nil, clears) when the channel's
// destination was verified. Update leaves verified_at alone so a config
// edit cannot mark a channel verified by echoing the field back.
func (s *NotificationChannelStore) SetVerifiedAt(ctx context.Context, id string, at *time.Time) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET verified_at = ? WHERE id = ?`,
		s.d.OptionalTimeArg(at), id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *NotificationChannelStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	return s.deletePhoneVerifications(ctx, id)
}

// channelInUse matches step_targets rows that send through the channel in
// the enclosing statement, counting only policies in the channel's own org
// (a reference from another org must not block, or reveal, anything).
const channelInUse = `EXISTS (
	SELECT 1 FROM step_targets st
	JOIN escalation_steps es ON es.id = st.step_id
	JOIN escalation_policies ep ON ep.id = es.policy_id
	WHERE st.channel_id = notification_channels.id
	  AND ep.org_id = notification_channels.org_id)`

func (s *NotificationChannelStore) DeleteIfUnused(ctx context.Context, id string) (found, deleted bool, err error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM notification_channels WHERE id = ? AND NOT `+channelInUse, id)
	if err != nil {
		return false, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if n > 0 {
		return true, true, s.deletePhoneVerifications(ctx, id)
	}
	var one int
	err = s.q.QueryRowContext(ctx, `SELECT 1 FROM notification_channels WHERE id = ?`, id).Scan(&one)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, false, nil
}

// deletePhoneVerifications drops the verification rows that reference the
// channel by id only, so a deleted channel leaves no live OTP behind.
func (s *NotificationChannelStore) deletePhoneVerifications(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM phone_verifications WHERE subject_type = 'channel' AND subject_id = ?`, id)
	return err
}

func (s *NotificationChannelStore) ListPolicyRefs(ctx context.Context, orgID, channelID string) (map[string][]domain.ChannelPolicyRef, error) {
	query := `SELECT st.channel_id, ep.id, ep.name, es.position
		 FROM step_targets st
		 JOIN escalation_steps es ON es.id = st.step_id
		 JOIN escalation_policies ep ON ep.id = es.policy_id
		 WHERE ep.org_id = ? AND st.channel_id <> ''`
	args := []any{orgID}
	if channelID != "" {
		query += ` AND st.channel_id = ?`
		args = append(args, channelID)
	}
	query += ` ORDER BY st.channel_id, ep.name, ep.id, es.position`
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]domain.ChannelPolicyRef{}
	for rows.Next() {
		var chID, policyID, name string
		var pos int
		if err := rows.Scan(&chID, &policyID, &name, &pos); err != nil {
			return nil, err
		}
		refs := out[chID]
		if n := len(refs); n == 0 || refs[n-1].PolicyID != policyID {
			refs = append(refs, domain.ChannelPolicyRef{PolicyID: policyID, PolicyName: name})
		}
		last := &refs[len(refs)-1]
		// A step can hold several targets on the same channel.
		if k := len(last.Steps); k == 0 || last.Steps[k-1] != pos+1 {
			last.Steps = append(last.Steps, pos+1)
		}
		out[chID] = refs
	}
	return out, rows.Err()
}

// RecordDeliverySuccess marks a successful delivery: it stamps
// last_success_at, resets the consecutive failure count and clears the
// failure notice. last_error and last_failure_at are kept as history.
// recovered reports whether this call cleared a notice; the conditional
// update lets only one concurrent caller see true.
func (s *NotificationChannelStore) RecordDeliverySuccess(ctx context.Context, id string, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET failure_notified_at = NULL
		 WHERE id = ? AND failure_notified_at IS NOT NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET last_success_at = ?, consecutive_failures = 0 WHERE id = ?`,
		s.d.TimeArg(at), id); err != nil {
		return false, err
	}
	return n == 1, nil
}

// ClaimFailureNotice claims the owner email for the channel's current
// failure streak. The WHERE clause carries every condition, so two
// replicas racing on the same streak cannot both win.
func (s *NotificationChannelStore) ClaimFailureNotice(ctx context.Context, id string, threshold int, at time.Time) (bool, error) {
	if threshold < 1 {
		threshold = 1
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET failure_notified_at = ?
		 WHERE id = ? AND failure_notified_at IS NULL AND consecutive_failures >= ?`,
		s.d.TimeArg(at), id, threshold)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ReleaseFailureNotice clears a claim made at at. A claim made later by
// someone else, or already cleared by a success, is left alone.
func (s *NotificationChannelStore) ReleaseFailureNotice(ctx context.Context, id string, at time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE notification_channels SET failure_notified_at = NULL
		 WHERE id = ? AND failure_notified_at = ?`,
		id, s.d.TimeArg(at))
	return err
}

// RecordDeliveryFailure records a failed delivery and returns the new
// consecutive failure count. reason must already be sanitized; it is
// trimmed and capped at domain.MaxChannelErrorLen here as a backstop.
// A missing channel (deleted since the job was queued) returns 0, nil.
func (s *NotificationChannelStore) RecordDeliveryFailure(ctx context.Context, id string, at time.Time, reason string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx,
		`UPDATE notification_channels
		 SET last_failure_at = ?, last_error = ?, consecutive_failures = consecutive_failures + 1
		 WHERE id = ?
		 RETURNING consecutive_failures`,
		s.d.TimeArg(at), capChannelError(reason), id).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// capChannelError trims reason to domain.MaxChannelErrorLen bytes without
// splitting a UTF-8 sequence.
func capChannelError(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) <= domain.MaxChannelErrorLen {
		return reason
	}
	cut := domain.MaxChannelErrorLen
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}
