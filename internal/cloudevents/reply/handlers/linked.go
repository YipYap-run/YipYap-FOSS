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

// allowedLinkKinds enumerates the downstream systems a reply may link to.
// Keeping this explicit prevents arbitrary free-text kinds from polluting
// the timeline. The "other" kind is provided as an escape hatch.
var allowedLinkKinds = map[string]struct{}{
	"jira":         {},
	"pagerduty":    {},
	"slack_thread": {},
	"github_issue": {},
	"gitlab_issue": {},
	"linear":       {},
	"servicenow":   {},
	"other":        {},
}

// LinkedPayload is the JSON shape persisted in AlertEvent.Detail for a
// reply.linked annotation.
type LinkedPayload struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	Actor string `json:"actor"`
	Sub   string `json:"sub,omitempty"`
}

// LinkedHandler annotates the alert timeline with a reference to an
// external ticket or conversation (Jira, PagerDuty, etc.). Read-only.
type LinkedHandler struct {
	store store.Store
}

// NewLinkedHandler returns a LinkedHandler backed by s.
func NewLinkedHandler(s store.Store) *LinkedHandler {
	return &LinkedHandler{store: s}
}

// Type returns the CE type string this handler registers against.
func (h *LinkedHandler) Type() string {
	return types.TypeReplyAlertLinkedV1
}

// Handle unmarshals the reply data, validates the link shape, and persists
// a timeline annotation.
func (h *LinkedHandler) Handle(ctx context.Context, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil {
		return fmt.Errorf("reply linked: nil registry entry")
	}

	var data types.ReplyAlertLinkedV1Data
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

	if _, ok := allowedLinkKinds[data.Kind]; !ok {
		return warnAndReject(ctx, h.store, ev, entry, sub, "invalid_kind",
			fmt.Sprintf("kind=%q", data.Kind))
	}
	if data.ID == "" {
		return warnAndReject(ctx, h.store, ev, entry, sub, "missing_id",
			"reply.linked requires data.id")
	}
	if err := enforceMaxRunes("id", data.ID, MaxReferenceIDChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	if err := enforceMaxRunes("title", data.Title, MaxTitleChars); err != nil {
		return warnAndReject(ctx, h.store, ev, entry, sub, "field_too_long", err.Error())
	}
	// A2-NM-2: enforce http/https scheme allowlist on linked.url.
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

	payload := LinkedPayload{
		Kind:  data.Kind,
		ID:    data.ID,
		URL:   data.URL,
		Title: data.Title,
		Actor: effectiveActor(ev, sub),
		Sub:   sub,
	}
	if err := writeTimelineEvent(ctx, h.store, alertID, domain.EventReplyLinked, payload); err != nil {
		writeAudit(ctx, h.store, ev, entry, sub, handlerErrorOutcome("write_timeline"), nil, nil)
		return err
	}
	writeAudit(ctx, h.store, ev, entry, sub, outcomeAccepted, nil, payload)
	return nil
}
