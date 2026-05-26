package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// writeTimelineEvent persists a read-only timeline annotation against the
// given alert. payload is JSON-encoded into the AlertEvent.Detail column.
//
// This is the single canonical code path the Phase-2 reply handlers use to
// write to the alert_events table. Keeping it centralised gives us one
// place to add metrics, retries, or bulk batching in the future.
func writeTimelineEvent(ctx context.Context, s store.Store, alertID string, evType domain.AlertEventType, payload any) error {
	if s == nil {
		return fmt.Errorf("reply handlers: store is nil")
	}
	if alertID == "" {
		return fmt.Errorf("reply handlers: alert id is empty")
	}

	detail, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("reply handlers: marshal detail: %w", err)
	}

	return s.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: evType,
		Detail:    json.RawMessage(detail),
		CreatedAt: time.Now().UTC(),
	})
}

