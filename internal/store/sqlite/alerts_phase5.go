package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MarkDuplicate sets alerts.duplicate_of = duplicateOfID and records a
// timeline event of type domain.EventDuplicated. When the caller has passed
// the store a transaction (Tx), both writes land atomically.
func (s *alertStore) MarkDuplicate(ctx context.Context, alertID, duplicateOfID, reason string) error {
	if alertID == "" || duplicateOfID == "" {
		return fmt.Errorf("MarkDuplicate: empty alert id or duplicate_of")
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET duplicate_of = ? WHERE id = ?`,
		duplicateOfID, alertID)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"duplicate_of": duplicateOfID,
		"reason":       reason,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventDuplicated,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})
}

// SetOwner updates alerts.owner_kind and alerts.owner_id and records a
// timeline event of type domain.EventOwnershipTransferred.
func (s *alertStore) SetOwner(ctx context.Context, alertID, ownerKind, ownerID, reason string) error {
	if alertID == "" {
		return fmt.Errorf("SetOwner: empty alert id")
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET owner_kind = ?, owner_id = ? WHERE id = ?`,
		ownerKind, ownerID, alertID)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"owner_kind": ownerKind,
		"owner_id":   ownerID,
		"reason":     reason,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventOwnershipTransferred,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})
}

// AckWithContext marks the alert acknowledged and stores the reason code,
// note, and external reference id. The note is surfaced via the timeline
// event detail rather than a column (alert_events already carries free-form
// detail blobs).
func (s *alertStore) AckWithContext(ctx context.Context, alertID, reasonCode, note, referenceID, ackedBy string) error {
	if alertID == "" {
		return fmt.Errorf("AckWithContext: empty alert id")
	}
	now := time.Now().UTC()
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts
		 SET status = 'acknowledged',
		     acknowledged_at = ?,
		     acknowledged_by = ?,
		     ack_reason_code = ?,
		     ack_reference_id = ?
		 WHERE id = ?`,
		now.Format(timeFormat), ackedBy, reasonCode, referenceID, alertID)
	if err != nil {
		return err
	}
	if err := expectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"reason_code":  reasonCode,
		"note":         note,
		"reference_id": referenceID,
		"acked_by":     ackedBy,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventAckWithContext,
		Detail:    detail,
		CreatedAt: now,
	})
}
