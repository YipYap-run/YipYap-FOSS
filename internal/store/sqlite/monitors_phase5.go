package sqlite

import (
	"context"
	"fmt"
	"time"
)

// Disable flips enabled=false on the given monitor. The reason is captured
// by the caller's audit log (via CloudEventReplyAudit), not here, the
// monitors table doesn't carry a disabled_reason column. Returns an error
// if no row with that id exists.
func (s *monitorStore) Disable(ctx context.Context, monitorID, reason string) error {
	if monitorID == "" {
		return fmt.Errorf("Disable: empty monitor id")
	}
	_ = reason // reason is surfaced via audit log, not mutated on monitors
	res, err := s.q.ExecContext(ctx,
		`UPDATE monitors SET enabled = 0, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(timeFormat), monitorID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}
