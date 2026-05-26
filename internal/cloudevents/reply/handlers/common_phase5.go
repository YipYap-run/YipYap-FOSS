package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// -- Reply field length caps ----------------------------------------------
//
// A2-NM-3: reply string fields and match-filter map keys were uncapped,
// bounded only by the 1 MiB body limit at ingest. A single reply could
// stuff a 900 KiB Reason into the audit log + timeline, blowing storage
// per-reply and degrading admin-dashboard latency. Cap each field at the
// smallest size that still serves real workflows.
const (
	// MaxReasonChars caps Reason on every reply (route, suppressed,
	// monitor.deregister, ownership, etc.).
	MaxReasonChars = 512
	// MaxNoteChars caps Note (claimed.note, ack_with_context.note, ...).
	MaxNoteChars = 2048
	// MaxDescriptionChars caps Description (remediation_started).
	MaxDescriptionChars = 2048
	// MaxMessageChars caps Message (remediation_result, public_message).
	MaxMessageChars = 2048
	// MaxTitleChars caps Title (linked.title, attachment label).
	MaxTitleChars = 256
	// MaxReferenceIDChars caps ReferenceID (ack_with_context.reference_id,
	// linked.id).
	MaxReferenceIDChars = 256
	// MaxURLChars caps any reply-supplied URL (artifact_url, status_page url,
	// linked url, enriched href).
	MaxURLChars = 2048
	// MaxMatchFilterKeys caps the number of keys in a route/suppressed
	// match_filter map.
	MaxMatchFilterKeys = 16
	// MaxMatchFilterValueChars caps each match_filter value (or each entry
	// of a []string value).
	MaxMatchFilterValueChars = 256
	// MaxAttachmentLabelChars caps EnrichedAttachment.Label.
	MaxAttachmentLabelChars = 256
	// MaxAttachmentPreviewChars caps EnrichedAttachment.Preview (data: URLs
	// can be long but should not be book-length).
	MaxAttachmentPreviewChars = 4096
	// MaxAttachmentKindChars caps EnrichedAttachment.Kind.
	MaxAttachmentKindChars = 64
	// MaxStatusPagePublicMessageBytes caps status_page.public_message in
	// rune units (utf8-safe). The previous byte-truncation could split a
	// multi-byte codepoint at the boundary.
	MaxStatusPagePublicMessageBytes = 2048
)

// allowedReplyURLSchemes enumerates the schemes accepted on any
// reply-supplied URL field. javascript:, data:, file: and friends were
// previously accepted because url.Parse alone treats them as valid.
var allowedReplyURLSchemes = map[string]struct{}{
	"http":  {},
	"https": {},
}

// auditOutcome is a thin type alias used by Phase-5 handlers to keep the
// audit-entry outcome strings consistent.
type auditOutcome string

const (
	outcomeAccepted auditOutcome = "accepted"
)

// rejectedOutcome returns the canonical "rejected:<reason>" audit outcome
// string.
func rejectedOutcome(reason string) auditOutcome {
	return auditOutcome("rejected:" + reason)
}

// handlerErrorOutcome returns the canonical "handler_error:<message>" audit
// outcome string.
func handlerErrorOutcome(msg string) auditOutcome {
	return auditOutcome("handler_error:" + msg)
}

// writeAudit records a Phase-5 reply-audit row against the channel's store.
// Swallows write errors after logging, audit failures must not block the
// handler's primary mutation from being visible to the caller.
func writeAudit(ctx context.Context, s store.Store, ev ce.Event, entry *reply.Entry, sub string, outcome auditOutcome, before, after any) {
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) == "null" {
		beforeJSON = []byte("{}")
	}
	if string(afterJSON) == "null" {
		afterJSON = []byte("{}")
	}

	e := &store.CloudEventReplyAuditEntry{
		ReplyID:     ev.ID(),
		ReplyType:   ev.Type(),
		ReplySource: ev.Source(),
		ReplySub:    sub,
		OrgID:       entryOrgID(entry),
		AlertID:     entryAlertID(entry),
		MonitorID:   entryMonitorID(entry),
		ChannelID:   entryChannelID(entry),
		Outcome:     string(outcome),
		Before:      beforeJSON,
		After:       afterJSON,
		ReceivedAt:  time.Now().UTC(),
	}
	if err := s.CloudEventReplyAudit().Write(ctx, e); err != nil {
		slog.Warn("reply audit write failed",
			"reply_type", ev.Type(),
			"reply_id", ev.ID(),
			"outcome", string(outcome),
			"err", err,
		)
	}
}

func entryOrgID(e *reply.Entry) string {
	if e == nil {
		return ""
	}
	return e.OrgID
}
func entryAlertID(e *reply.Entry) string {
	if e == nil {
		return ""
	}
	return e.AlertID
}
func entryMonitorID(e *reply.Entry) string {
	if e == nil {
		return ""
	}
	return e.MonitorID
}
func entryChannelID(e *reply.Entry) string {
	if e == nil {
		return ""
	}
	return e.ChannelID
}

// effectiveAlertID returns the canonical alert_id for the reply (entry
// takes precedence; data.alert_id is accepted when the entry has none, e.g.
// monitor/maintenance replies). Returns "" if neither has an id.
//
// Callers must guard cross-alert hijacks with a prior equality check; this
// helper only resolves the id to use for downstream lookups once that gate
// has passed.
//
// IMPORTANT: Alert-scoped handlers MUST NOT trust the returned id without
// first calling loadAndVerifyAlert below (or otherwise rejecting when the
// entry is monitor-scoped, entry.AlertID == ""). Otherwise a sink holding
// any monitor-level alertref can supply an arbitrary data.AlertID and
// mutate alerts in other orgs.
func effectiveAlertID(entryAlertID, dataAlertID string) string {
	if entryAlertID != "" {
		return entryAlertID
	}
	return dataAlertID
}

// loadAndVerifyAlert loads the alert by id and verifies it belongs to the
// same org as the registry entry. Returns the alert on success; returns a
// rejected-sentinel error on mismatch (after writing the audit row) or a
// wrapped GetByID error when the alert can't be loaded, and writes a
// rejected:alert_not_found audit row in the latter case so a probe of
// arbitrary alert ids can't distinguish "exists in another org" from "does
// not exist" by audit-row presence (A3-NEW-M-1).
//
// Precondition: caller has already verified the handler is alert-scoped via
// requireAlertScopedEntry (which rejects replies with an empty entry.AlertID
// for alert-mutation handlers).
func loadAndVerifyAlert(ctx context.Context, s store.Store, ev ce.Event, entry *reply.Entry, sub, alertID string) (*domain.Alert, error) {
	alert, err := s.Alerts().GetByID(ctx, alertID)
	if err != nil {
		// Fingerprint-divergence defence: write a rejected audit row even
		// when the alert isn't found. Without this, an attacker probing
		// alert IDs sees a different observable (no audit row) than for a
		// cross-org alert (audit row present), enabling enumeration.
		writeAudit(ctx, s, ev, entry, sub, rejectedOutcome("alert_not_found"), nil, nil)
		return nil, fmt.Errorf("reply %s: load alert: %w", ev.Type(), err)
	}
	if entry != nil && entry.OrgID != "" && alert.OrgID != entry.OrgID {
		return nil, warnAndReject(ctx, s, ev, entry, sub, "cross_org_alert",
			fmt.Sprintf("alert.org=%q entry.org=%q", alert.OrgID, entry.OrgID))
	}
	return alert, nil
}

// effectiveActor resolves the non-spoofable actor identity for an audit /
// timeline write. Rules:
//
//   - If the OIDC-verified subject (sub) is non-empty, use it directly.
//   - Otherwise, return a synthetic id derived from the CE envelope id,
//     "cloudevent:reply:<event_id>", so the audit trail records that
//     the action came from the reply channel, not a real user.
//
// Sink-supplied attribution (e.g. data.Who on claimed.v1) MUST NOT be
// passed in here, those values can be recorded as descriptive labels but
// are never the actor identity. (A3-NEW-M-3.)
func effectiveActor(ev ce.Event, sub string) string {
	if sub != "" {
		return sub
	}
	return "cloudevent:reply:" + ev.ID()
}

// validateMatchFilterValues rejects match_filter entries that are not
// non-empty strings (or non-empty []string of non-empty strings). Earlier
// behaviour only caught nil + whitespace strings, leaving falsy non-string
// values (bool false, 0, []any{}, map[string]any{}) as untyped backdoors a
// naive Phase-6 matcher could interpret as wildcards. Phase-6 consumer
// simplicity: filter values are strings (or []string) only.
//
// Also enforces MaxMatchFilterKeys + MaxMatchFilterValueChars (A2-NM-3).
func validateMatchFilterValues(mf map[string]any) error {
	if len(mf) > MaxMatchFilterKeys {
		return fmt.Errorf("match_filter has %d keys (max %d)", len(mf), MaxMatchFilterKeys)
	}
	for k, v := range mf {
		if k == "" || strings.TrimSpace(k) == "" {
			return fmt.Errorf("match_filter has empty/whitespace key")
		}
		if utf8.RuneCountInString(k) > MaxMatchFilterValueChars {
			return fmt.Errorf("match_filter key %q exceeds %d chars", k, MaxMatchFilterValueChars)
		}
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) == "" {
				return fmt.Errorf("match_filter %q has empty/whitespace value", k)
			}
			if utf8.RuneCountInString(x) > MaxMatchFilterValueChars {
				return fmt.Errorf("match_filter %q value exceeds %d chars", k, MaxMatchFilterValueChars)
			}
		case []string:
			if len(x) == 0 {
				return fmt.Errorf("match_filter %q is an empty []string", k)
			}
			for i, item := range x {
				if strings.TrimSpace(item) == "" {
					return fmt.Errorf("match_filter %q[%d] is empty/whitespace", k, i)
				}
				if utf8.RuneCountInString(item) > MaxMatchFilterValueChars {
					return fmt.Errorf("match_filter %q[%d] exceeds %d chars", k, i, MaxMatchFilterValueChars)
				}
			}
		case []any:
			// Permitted only when every element is a non-empty string
			// (JSON unmarshal of []string into map[string]any). Anything
			// else is rejected, no structured types in filters.
			if len(x) == 0 {
				return fmt.Errorf("match_filter %q is an empty list", k)
			}
			for i, item := range x {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("match_filter %q[%d] is %T, want string", k, i, item)
				}
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("match_filter %q[%d] is empty/whitespace", k, i)
				}
				if utf8.RuneCountInString(s) > MaxMatchFilterValueChars {
					return fmt.Errorf("match_filter %q[%d] exceeds %d chars", k, i, MaxMatchFilterValueChars)
				}
			}
		default:
			// nil, bool, numbers, maps, etc., all rejected.
			return fmt.Errorf("match_filter %q has non-string value (%T)", k, v)
		}
	}
	return nil
}

// validateReplyURL enforces the http/https scheme allowlist on any
// reply-supplied URL field. javascript:, data:, file:, and other schemes
// pass url.Parse with no error today; rejecting at the handler boundary
// (A2-NM-2) blocks a sink from planting a "https-looking" XSS payload via
// the timeline UI. Empty input is permitted, fields are optional unless
// the caller has already enforced presence.
func validateReplyURL(raw string) error {
	if raw == "" {
		return nil
	}
	if utf8.RuneCountInString(raw) > MaxURLChars {
		return fmt.Errorf("url exceeds %d chars", MaxURLChars)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url parse: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := allowedReplyURLSchemes[scheme]; !ok {
		return fmt.Errorf("url scheme %q not allowed (only http/https)", scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url missing host")
	}
	return nil
}

// truncateUTF8Runes returns s truncated to at most maxBytes bytes, but
// never splits a multi-byte UTF-8 codepoint. The previous byte-slice
// approach (msg[:cap]) could leave a truncated 4-byte rune that fails
// downstream JSON / charset validators. Rune-aware truncation keeps the
// resulting string valid UTF-8 with byte length ≤ maxBytes.
func truncateUTF8Runes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	// Walk runes from the start, accumulating bytes until adding the next
	// rune would exceed the cap. This is O(n) but bounded by maxBytes.
	out := make([]byte, 0, maxBytes)
	for _, r := range s {
		rb := utf8.RuneLen(r)
		if rb < 0 {
			rb = 1
		}
		if len(out)+rb > maxBytes {
			break
		}
		buf := make([]byte, rb)
		utf8.EncodeRune(buf, r)
		out = append(out, buf...)
	}
	return string(out)
}

// enforceMaxRunes rejects strings whose rune count exceeds maxRunes. Used
// for capped reply fields (Reason, Note, Description, Message, Title,
// ReferenceID) so the cap is consistent regardless of multi-byte content.
// Empty strings always pass (callers that require presence check separately).
func enforceMaxRunes(name, s string, maxRunes int) error {
	if utf8.RuneCountInString(s) > maxRunes {
		return fmt.Errorf("%s exceeds %d chars", name, maxRunes)
	}
	return nil
}

// requireAlertScopedEntry rejects a reply whose registry entry is NOT
// alert-scoped (entry.AlertID == ""). This is the single place that closes
// the monitor-level-alertref hijack for alert-mutating handlers:
//
//   - A sink receives a run.yipyap.monitor.down.v1 outbound. The registry
//     entry has OrgID set but AlertID == "" (monitor events carry no alert_id).
//   - The sink replies with reply.alert.<anything>.v1 and supplies
//     data.AlertID = "alert-in-another-org".
//   - Without this guard, effectiveAlertID returns the attacker's id and the
//     handler mutates a foreign alert.
//
// Alert-scoped handlers (suppressed, deduplicated, escalated, ownership,
// ack_with_context, remediation_*, route, status_page) MUST call this before
// resolving effectiveAlertID.
func requireAlertScopedEntry(ctx context.Context, s store.Store, ev ce.Event, entry *reply.Entry, sub string) error {
	if entry == nil || entry.AlertID == "" {
		return warnAndReject(ctx, s, ev, entry, sub, "alert_scope_required",
			"alert-mutating reply requires an alert-scoped entry; monitor-level alertref rejected")
	}
	return nil
}

// warnAndReject logs the rejection via slog and writes an audit row tagged
// "rejected:<reason>". Returns a wrapped error so the dispatcher's caller
// sees the failure.
func warnAndReject(ctx context.Context, s store.Store, ev ce.Event, entry *reply.Entry, sub, reason, detail string) error {
	slog.Warn("reply rejected",
		"reply_type", ev.Type(),
		"reply_id", ev.ID(),
		"reason", reason,
		"detail", detail,
	)
	writeAudit(ctx, s, ev, entry, sub, rejectedOutcome(reason), nil, nil)
	return fmt.Errorf("reply %s: %s: %s", ev.Type(), reason, detail)
}
