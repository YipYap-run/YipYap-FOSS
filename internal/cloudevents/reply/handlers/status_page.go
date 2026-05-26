package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// allowedStatusPageProviders enumerates the external status-page providers
// we accept references for. Unknown values are rejected and logged.
var allowedStatusPageProviders = map[string]struct{}{
	"statuspage_io":   {},
	"cachet":          {},
	"instatus":        {},
	"atlassian_sb":    {},
	"incidentio":      {},
	"statusgator":     {},
	"better_uptime":   {},
	"openstatus":      {},
	"yipyap_internal": {},
	"other":           {},
}

// StatusPageHandler implements reply.alert.status_page.v1. It records an
// external status-page reference against the alert so the UI can display a
// public-status-page link alongside the alert.
type StatusPageHandler struct {
	store store.Store
}

// NewStatusPageHandler returns a StatusPageHandler backed by s.
func NewStatusPageHandler(s store.Store) *StatusPageHandler {
	return &StatusPageHandler{store: s}
}

// Type returns the CE type this handler registers against.
func (h *StatusPageHandler) Type() string { return types.TypeReplyAlertStatusPageV1 }

func (h *StatusPageHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply status_page: nil registry entry")
	}
	var data types.ReplyAlertStatusPageV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if entry.AlertID != "" && data.AlertID != "" && data.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_alert",
			fmt.Sprintf("data.alert_id=%q entry.alert_id=%q", data.AlertID, entry.AlertID))
	}
	if err := requireAlertScopedEntry(ctx, h.store, ev, entry, sub); err != nil {
		return err
	}
	if _, ok := allowedStatusPageProviders[data.Provider]; !ok {
		return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_provider",
			fmt.Sprintf("provider=%q", data.Provider))
	}
	// A2-NM-2: status_page.url accepts only http/https.
	if err := validateReplyURL(data.URL); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "url_scheme", err.Error())
	}
	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}

	// Round-1 residual: rune-aware truncation prevents splitting a
	// multi-byte UTF-8 codepoint at the byte cap.
	msg := truncateUTF8Runes(data.PublicMessage, MaxStatusPagePublicMessageBytes)

	// updated_at is sink-controlled; keep handler-side default to NOW when
	// absent. The store-side already overwrites the row's persisted
	// updated_at on subsequent edits, but coordinate with sibling agent 5
	// who is moving final ownership of updated_at into the store layer.
	updatedAt := data.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	row := &store.AlertStatusPageRef{
		ID:            uuid.New().String(),
		AlertID:       alertID,
		Provider:      data.Provider,
		URL:           data.URL,
		PublicMessage: msg,
		UpdatedAt:     updatedAt,
		SourceReplyID: ev.ID(),
	}
	if err := h.store.AlertStatusPageRefs().Create(ctx, row); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("create_status_page_ref"), nil, nil)
		return fmt.Errorf("reply status_page: create: %w", err)
	}

	detail, _ := json.Marshal(map[string]any{
		"ref_id":   row.ID,
		"provider": data.Provider,
		"url":      data.URL,
		"actor":    effectiveActor(ev, sub),
		"sub":      sub,
	})
	_ = h.store.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventStatusPageSynced,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})

	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, row)
	return nil
}
