package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// maxEnrichedAttachments caps the number of attachments a single reply may
// carry. Bounds storage and rendering cost and makes abusive replies
// easier to spot.
const maxEnrichedAttachments = 20

// EnrichedAttachment is the normalised attachment form we persist, we do
// not trust arbitrary keys from the reply's free-form map.
type EnrichedAttachment struct {
	Label   string `json:"label"`
	Href    string `json:"href,omitempty"`
	Preview string `json:"preview,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// EnrichedPayload is the JSON shape persisted in AlertEvent.Detail for a
// reply.enriched annotation.
type EnrichedPayload struct {
	Attachments []EnrichedAttachment `json:"attachments"`
	Actor       string               `json:"actor"`
	Sub         string               `json:"sub,omitempty"`
}

// EnrichedHandler annotates the alert timeline with attachments provided
// by a downstream consumer (e.g. runbook links, screenshots). Read-only.
type EnrichedHandler struct {
	store store.Store
}

// NewEnrichedHandler returns an EnrichedHandler backed by s.
func NewEnrichedHandler(s store.Store) *EnrichedHandler {
	return &EnrichedHandler{store: s}
}

// Type returns the CE type string this handler registers against.
func (h *EnrichedHandler) Type() string {
	return types.TypeReplyAlertEnrichedV1
}

// Handle unmarshals the reply data, validates each attachment, and persists
// a timeline annotation.
func (h *EnrichedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply enriched: nil registry entry")
	}

	var data types.ReplyAlertEnrichedV1Data
	if err := json.Unmarshal(ev.Data(), &data); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "malformed_data", err.Error())
	}
	if entry.AlertID != "" && data.AlertID != "" && data.AlertID != entry.AlertID {
		return warnAndReject(ctx, h.store, ev, entry, sub, "cross_alert",
			fmt.Sprintf("data.alert_id=%q entry.alert_id=%q", data.AlertID, entry.AlertID))
	}
	// R2-H5: Phase-2 handlers must close the monitor-level-alertref pivot.
	if err := requireAlertScopedEntry(ctx, h.store, ev, entry, sub); err != nil {
		return err
	}

	if len(data.Attachments) > maxEnrichedAttachments {
		return warnAndReject(ctx, h.store, ev, entry, sub, "too_many_attachments",
			fmt.Sprintf("%d > %d", len(data.Attachments), maxEnrichedAttachments))
	}

	normalised := make([]EnrichedAttachment, 0, len(data.Attachments))
	for i, raw := range data.Attachments {
		att, err := normaliseAttachment(i, raw)
		if err != nil {
			return warnAndReject(ctx, h.store, ev, entry, sub, "attachment_invalid", err.Error())
		}
		normalised = append(normalised, att)
	}

	alertID := effectiveAlertID(entry.AlertID, data.AlertID)
	if alertID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_alert_id", "")
	}
	if _, err := loadAndVerifyAlert(ctx, h.store, ev, entry, sub, alertID); err != nil {
		return err
	}

	payload := EnrichedPayload{
		Attachments: normalised,
		Actor:       effectiveActor(ev, sub),
		Sub:         sub,
	}
	if err := writeTimelineEvent(ctx, h.store, alertID, domain.EventReplyEnriched, payload); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("write_timeline"), nil, nil)
		return err
	}
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, payload)
	return nil
}

func normaliseAttachment(idx int, raw map[string]any) (EnrichedAttachment, error) {
	label, _ := raw["label"].(string)
	if label == "" {
		return EnrichedAttachment{}, fmt.Errorf("attachment[%d] missing required 'label'", idx)
	}
	if err := enforceMaxRunes("label", label, MaxAttachmentLabelChars); err != nil {
		return EnrichedAttachment{}, fmt.Errorf("attachment[%d] %w", idx, err)
	}
	href, _ := raw["href"].(string)
	if href != "" {
		// A2-NM-2: enforce http/https scheme allowlist + length cap.
		if err := validateReplyURL(href); err != nil {
			return EnrichedAttachment{}, fmt.Errorf("attachment[%d] href: %w", idx, err)
		}
	}
	preview, _ := raw["preview"].(string)
	if err := enforceMaxRunes("preview", preview, MaxAttachmentPreviewChars); err != nil {
		return EnrichedAttachment{}, fmt.Errorf("attachment[%d] %w", idx, err)
	}
	kind, _ := raw["kind"].(string)
	if err := enforceMaxRunes("kind", kind, MaxAttachmentKindChars); err != nil {
		return EnrichedAttachment{}, fmt.Errorf("attachment[%d] %w", idx, err)
	}
	return EnrichedAttachment{
		Label:   label,
		Href:    href,
		Preview: preview,
		Kind:    kind,
	}, nil
}
