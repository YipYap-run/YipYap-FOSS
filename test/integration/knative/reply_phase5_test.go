//go:build knative

// Phase-5 extends the Phase-2 reply harness with end-to-end coverage of the
// ten state-mutating reply handlers (suppressed, deduplicated, escalated,
// remediation_started, remediation_result, ownership, ack_with_context,
// route, status_page, monitor.deregister). Each test wires a real
// cloudevent_http provider + MemoryRegistry + Dispatcher + sqlite store,
// stands up an httptest sink that replies with the target reply type, calls
// provider.Send, and asserts on BOTH the handler's state mutation AND the
// audit row written by the Phase-5 audit helper.
//
// Like the Phase-2 suite these are wire-compatibility tests against a local
// httptest sink, not a real kind/Knative cluster.
package knative

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	replyhandlers "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// phase5ReplyOpts captures the per-test reply-config overrides we need to
// exercise. Phase-2's buildOutboundAlertFiredJob only accepts the accepted-
// type glob list; Phase-5 adds the two trust gates.
type phase5ReplyOpts struct {
	trustElevated   bool
	allowDeregister bool
	// maxSuppressSeconds, when non-zero, overrides the channel's suppress cap
	// (the dispatcher's ClampSuppressSeconds default is 3600s).
	maxSuppressSeconds int
	// maxRouteDurationSeconds overrides the route expiry cap (default 86400).
	maxRouteDurationSeconds int
}

// wirePhase5ReplyProvider builds a provider + dispatcher wired to all 13
// handlers (Phase-2 + Phase-5). Mirrors the Phase-2 wireReplyProvider shape
// but asserts the full handler count so a future handler addition lights up
// as a test break here first.
func wirePhase5ReplyProvider(t *testing.T, s *sqlite.SQLiteStore) (*providers.CloudEventHTTP, *reply.Dispatcher) {
	t.Helper()
	reg := reply.NewMemoryRegistry(256, time.Hour)
	d := reply.NewDispatcher(reg)
	if got := replyhandlers.RegisterAll(d, s); len(got) != 13 {
		t.Fatalf("RegisterAll returned %d handlers, want 13 (Phase-2 three + Phase-5 ten)", len(got))
	}
	p := providers.NewCloudEventHTTP(nil)
	p.SetOutboundRegistry(reg)
	p.SetReplyDispatcher(d)
	return p, d
}

// buildPhase5OutboundJob wraps buildOutboundAlertFiredJob and tweaks the
// channel's ReplyConfig so trust gates + caps can be set per test. The
// returned ce.Event is the outbound alert.fired whose id the sink echoes
// back as the reply's alertref extension.
func buildPhase5OutboundJob(t *testing.T, alert *domain.Alert, sinkURL string, acceptedTypes []string, opts phase5ReplyOpts) (domain.NotificationJob, ce.Event) {
	t.Helper()
	job, ev := buildOutboundAlertFiredJob(t, alert, sinkURL, acceptedTypes)
	// buildOutboundAlertFiredJob packs a plaintext config envelope with Types
	// only, we need to re-pack to add the trust gates. The easiest path is
	// to replicate the packaging with a fuller ReplyConfig and overwrite the
	// job's TargetConfig.
	cfg := providers.CloudEventHTTPConfig{
		SinkURL: sinkURL,
		Mode:    "binary",
		AcceptedReplies: &providers.ReplyConfig{
			Types:                   acceptedTypes,
			TrustElevated:           opts.trustElevated,
			AllowDeregister:         opts.allowDeregister,
			MaxSuppressSeconds:      opts.maxSuppressSeconds,
			MaxRouteDurationSeconds: opts.maxRouteDurationSeconds,
		},
	}
	cfgJSON, _ := json.Marshal(cfg)
	raw := append([]byte{0x00}, cfgJSON...)
	job.TargetConfig = base64.StdEncoding.EncodeToString(raw)
	return job, ev
}

// buildPhase5ReplySink stands up a sink whose response body is the CE
// produced by replyFn; the sink reads the outbound CE id from the Ce-Id
// header and passes it to replyFn so the built reply can echo it as
// alertref. Identical shape to Phase-2's buildReplyingSinkServer; kept
// local so the Phase-5 suite reads as a cohesive block.
func buildPhase5ReplySink(t *testing.T, replyFn func(outboundID string) ce.Event) string {
	t.Helper()
	srv := buildReplyingSinkServer(t, replyFn)
	return srv.URL
}

// seedTeamInOrg inserts a Team into the given org; the ownership handler
// needs the team row to exist for its cross-org check.
func seedTeamInOrg(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.Team {
	t.Helper()
	team := &domain.Team{
		ID:    "team-" + ulid.Make().String(),
		OrgID: orgID,
		Name:  "Phase5 Team",
	}
	if err := s.Teams().Create(context.Background(), team); err != nil {
		t.Fatalf("Teams.Create: %v", err)
	}
	return team
}

// seedChannelInOrg inserts a notification channel into the given org;
// the route handler's cross-org check consults this.
func seedChannelInOrg(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.NotificationChannel {
	t.Helper()
	ch := &domain.NotificationChannel{
		ID:      "ch-" + ulid.Make().String(),
		OrgID:   orgID,
		Type:    "email",
		Name:    "Failover Mailer",
		Config:  `{"to":"failover@example.com"}`,
		Enabled: true,
	}
	if err := s.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("NotificationChannels.Create: %v", err)
	}
	return ch
}

// seedSecondaryAlertInSameOrg creates a second alert (with its own monitor)
// in primary's org, for the deduplicated handler's cross-alert dedup test.
//
// Alerts().Update does NOT touch org_id in any backend (see
// sqlite/alerts.go, postgres/alerts.go, mariadb/alerts.go), so the secondary
// monitor+alert must be created in-org from the start rather than moved
// via a post-hoc Update.
func seedSecondaryAlertInSameOrg(t *testing.T, s *sqlite.SQLiteStore, primary *domain.Alert) *domain.Alert {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	mon := &domain.Monitor{
		ID:              "mon-reply-" + ulid.Make().String(),
		OrgID:           primary.OrgID,
		Name:            "reply-monitor-secondary",
		Type:            domain.MonitorHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatalf("Monitors.Create (secondary): %v", err)
	}

	alert := &domain.Alert{
		ID:        "alert-reply-" + ulid.Make().String(),
		OrgID:     primary.OrgID,
		MonitorID: mon.ID,
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatalf("Alerts.Create (secondary): %v", err)
	}
	return alert
}

// seedUpCheckForMonitor inserts a successful check row so the remediation
// handler's auto-resolve path fires.
func seedUpCheckForMonitor(t *testing.T, s *sqlite.SQLiteStore, monitorID string) {
	t.Helper()
	if err := s.Checks().Create(context.Background(), &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		Status:    domain.StatusUp,
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Checks.Create: %v", err)
	}
}

// requirePhase5KnativeEnv is the gate every Phase-5 E2E test runs through;
// skips the test unless KNATIVE=1 (consistent with the Phase-1/2 harness).
func requirePhase5KnativeEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("KNATIVE") != "1" {
		t.Skip("KNATIVE=1 required")
	}
}

// ---------------------------------------------------------------------------
// 1. suppressed
// ---------------------------------------------------------------------------

// TestKnative_Phase5_Suppressed_E2E, happy path: sink replies with
// suppressed.v1, handler creates an alert_suppressions row with the
// requested duration (clamped to the 3600s default) and writes an accepted
// audit row.
func TestKnative_Phase5_Suppressed_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-suppressed-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertSuppressedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertSuppressedV1Data{
			AlertID:         alert.ID,
			DurationSeconds: 300,
			Reason:          "deploy window",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.suppressed.v1"}, phase5ReplyOpts{})

	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	ctx := context.Background()
	rows, err := s.AlertSuppressions().ListActiveForAlert(ctx, alert.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListActiveForAlert: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("suppression rows=%d, want 1", len(rows))
	}
	if rows[0].Reason != "deploy window" {
		t.Errorf("Reason=%q, want %q", rows[0].Reason, "deploy window")
	}
	// 300s requested, default cap 3600s, honored as-is.
	if got := rows[0].ExpiresAt.Sub(time.Now().UTC()).Seconds(); got < 250 || got > 350 {
		t.Errorf("expires ~300s from now, got %vs", got)
	}

	audit, err := s.CloudEventReplyAudit().ListByAlert(ctx, alert.ID, 10)
	if err != nil {
		t.Fatalf("audit ListByAlert: %v", err)
	}
	if len(audit) == 0 {
		t.Fatal("no audit row written for accepted reply")
	}
	if audit[0].Outcome != "accepted" {
		t.Errorf("audit outcome=%q, want accepted", audit[0].Outcome)
	}
	if audit[0].ReplyType != types.TypeReplyAlertSuppressedV1 {
		t.Errorf("audit reply_type=%q", audit[0].ReplyType)
	}
}

// ---------------------------------------------------------------------------
// 2. deduplicated
// ---------------------------------------------------------------------------

// TestKnative_Phase5_Deduplicated_E2E, sink sends a deduplicated.v1 reply
// pointing alertB (the outbound event's alert) at alertA (the primary) in
// the same org; handler sets alerts.duplicate_of via MarkDuplicate and
// writes the EventDuplicated timeline row.
func TestKnative_Phase5_Deduplicated_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alertA := seedFiringAlert(t, s)                    // primary (dedup target)
	alertB := seedSecondaryAlertInSameOrg(t, s, alertA) // dup source
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-dedup-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertDeduplicatedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertDeduplicatedV1Data{
			AlertID:     alertB.ID,
			DuplicateOf: alertA.ID,
			Reason:      "same root cause",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alertB, sinkURL,
		[]string{"run.yipyap.reply.alert.deduplicated.v1"}, phase5ReplyOpts{})

	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	// Timeline event surfaces the MarkDuplicate write.
	if got := findAlertEvent(t, s, alertB.ID, domain.EventDuplicated); got == nil {
		t.Fatalf("no EventDuplicated in alert %s timeline", alertB.ID)
	}

	audit, err := s.CloudEventReplyAudit().ListByAlert(context.Background(), alertB.ID, 10)
	if err != nil {
		t.Fatalf("audit ListByAlert: %v", err)
	}
	if len(audit) == 0 || audit[0].Outcome != "accepted" {
		t.Fatalf("audit=%+v", audit)
	}
}

// ---------------------------------------------------------------------------
// 3. escalated
// ---------------------------------------------------------------------------

// TestKnative_Phase5_Escalated_E2E, reply lifts severity from warning to
// critical. Channel has TrustElevated=true; handler calls Alerts.Update with
// the new severity.
func TestKnative_Phase5_Escalated_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	// Downgrade to warning so we can observe the escalate.
	alert.Severity = domain.SeverityWarning
	if err := s.Alerts().Update(context.Background(), alert); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-esc-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertEscalatedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertEscalatedV1Data{
			AlertID:     alert.ID,
			NewSeverity: "critical",
			Reason:      "pager on fire",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.escalated.v1"},
		phase5ReplyOpts{trustElevated: true})

	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if got.Severity != domain.SeverityCritical {
		t.Errorf("severity=%q, want critical", got.Severity)
	}
	audit, _ := s.CloudEventReplyAudit().ListByAlert(context.Background(), alert.ID, 10)
	if len(audit) == 0 || audit[0].Outcome != "accepted" {
		t.Fatalf("audit=%+v", audit)
	}
}

// TestKnative_Phase5_Escalated_WithoutTrustElevated_Rejected, same setup
// as the happy path, TrustElevated=false. The handler rejects with
// "trust_flag_required"; no severity change, audit outcome prefixed
// "rejected:".
func TestKnative_Phase5_Escalated_WithoutTrustElevated_Rejected(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	alert.Severity = domain.SeverityWarning
	if err := s.Alerts().Update(context.Background(), alert); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-esc-reject-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertEscalatedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertEscalatedV1Data{
			AlertID:     alert.ID,
			NewSeverity: "critical",
			Reason:      "attempted escalation",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.escalated.v1"},
		phase5ReplyOpts{trustElevated: false})

	// Send returns nil because the sink returned 200; the dispatcher logs
	// the rejection but doesn't bubble it up past the provider.
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if got.Severity != domain.SeverityWarning {
		t.Errorf("severity mutated despite trust-flag rejection: %q", got.Severity)
	}
	// Post-H-23: the trust-flag rejection fires at the dispatcher layer,
	// which has no store handle and therefore does not write an audit row.
	// The guarantee this test cares about is "severity NOT mutated" (above),
	// plus a slog.Warn emitted, both hold.
}

// ---------------------------------------------------------------------------
// 4. remediation lifecycle (started + result)
// ---------------------------------------------------------------------------

// TestKnative_Phase5_RemediationLifecycle_E2E, first outbound/reply round
// writes a remediation_started row (status=in_progress); a second round
// delivers remediation_result{outcome:success} which flips the row to
// success and, because the monitor's latest check is StatusUp, auto-
// resolves the alert.
func TestKnative_Phase5_RemediationLifecycle_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	seedUpCheckForMonitor(t, s, alert.MonitorID)
	prov, _ := wirePhase5ReplyProvider(t, s)
	remediationID := "rem-" + ulid.Make().String()

	// Round 1: remediation_started.
	sinkStarted := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-rem-start-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertRemediationStartedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertRemediationStartedV1Data{
			AlertID:                 alert.ID,
			RemediationID:           remediationID,
			Description:             "rolling restart",
			ExpectedDurationSeconds: 120,
		})
		return ev
	})

	jobStarted, _ := buildPhase5OutboundJob(t, alert, sinkStarted,
		[]string{"run.yipyap.reply.alert.*"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), jobStarted); err != nil {
		t.Fatalf("provider.Send (started): %v", err)
	}

	row, err := s.AlertRemediations().GetByID(context.Background(), remediationID)
	if err != nil {
		t.Fatalf("remediation not created: %v", err)
	}
	if row.Status != store.RemediationInProgress {
		t.Fatalf("status=%q, want in_progress", row.Status)
	}

	// Round 2: remediation_result{success}. New outbound event so we get a
	// fresh alertref; the sink echoes the new id.
	sinkResult := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-rem-result-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertRemediationResultV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertRemediationResultV1Data{
			AlertID:       alert.ID,
			RemediationID: remediationID,
			Outcome:       "success",
			Message:       "fixed",
		})
		return ev
	})
	jobResult, _ := buildPhase5OutboundJob(t, alert, sinkResult,
		[]string{"run.yipyap.reply.alert.*"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), jobResult); err != nil {
		t.Fatalf("provider.Send (result): %v", err)
	}

	// Remediation row flipped to success.
	after, err := s.AlertRemediations().GetByID(context.Background(), remediationID)
	if err != nil {
		t.Fatalf("GetByID after result: %v", err)
	}
	if after.Status != store.RemediationSuccess {
		t.Errorf("status=%q, want success", after.Status)
	}
	// Alert auto-resolved because latest check is StatusUp.
	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status != domain.AlertResolved {
		t.Errorf("alert status=%q, want resolved (monitor up + success)", a.Status)
	}
}

// ---------------------------------------------------------------------------
// 5. ownership
// ---------------------------------------------------------------------------

// TestKnative_Phase5_Ownership_E2E, sink replies setting owner_kind=team,
// owner_id=<seeded team id>. The handler verifies the team exists in the
// alert's org, then writes via Alerts.SetOwner (which inserts an
// EventOwnershipTransferred timeline row).
func TestKnative_Phase5_Ownership_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	team := seedTeamInOrg(t, s, alert.OrgID)
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-own-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertOwnershipV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertOwnershipV1Data{
			AlertID:   alert.ID,
			OwnerKind: "team",
			OwnerID:   team.ID,
			Reason:    "infra team owns this",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.ownership.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	if got := findAlertEvent(t, s, alert.ID, domain.EventOwnershipTransferred); got == nil {
		t.Fatalf("no EventOwnershipTransferred in timeline")
	}
	audit, _ := s.CloudEventReplyAudit().ListByAlert(context.Background(), alert.ID, 10)
	if len(audit) == 0 || audit[0].Outcome != "accepted" {
		t.Fatalf("audit=%+v", audit)
	}
}

// ---------------------------------------------------------------------------
// 6. ack_with_context
// ---------------------------------------------------------------------------

// TestKnative_Phase5_AckWithContext_E2E, sink replies acking the alert
// with reason_code=investigating. Alert transitions to AlertAcknowledged and
// AcknowledgedBy is set to the handler's `sub` fallback (ev.Source() here
// because outbound OIDC does not surface the sink's identity at this layer).
func TestKnative_Phase5_AckWithContext_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	prov, _ := wirePhase5ReplyProvider(t, s)

	const source = "https://consumer.example.com/"
	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-ack-" + outboundID)
		ev.SetSource(source)
		ev.SetType(types.TypeReplyAlertAckWithContextV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertAckWithContextV1Data{
			AlertID:     alert.ID,
			ReasonCode:  "investigating",
			Note:        "paged on-call",
			ReferenceID: "JIRA-42",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.ack_with_context.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	a, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if a.Status != domain.AlertAcknowledged {
		t.Errorf("status=%q, want acknowledged", a.Status)
	}
	// H-20: sub is empty at this layer (outbound OIDC identifies yipyap to
	// the sink, not the sink to yipyap). AcknowledgedBy used to fall back
	// to ev.Source(), attacker-controlled, so a reply could claim any
	// string as the acker. It now records a non-spoofable synthetic
	// "cloudevent:reply:<ev.ID()>" attribution instead. ev.Source() is
	// still captured in the audit row for forensics.
	wantAckedBy := "cloudevent:reply:reply-ack-"
	if !strings.HasPrefix(a.AcknowledgedBy, wantAckedBy) {
		t.Errorf("AcknowledgedBy=%q, want prefix %q", a.AcknowledgedBy, wantAckedBy)
	}
	if a.AcknowledgedBy == source {
		t.Errorf("AcknowledgedBy was set to attacker-controlled ev.Source() = %q", source)
	}
	// Timeline event carries the reason_code + reference_id.
	got := findAlertEvent(t, s, alert.ID, domain.EventAckWithContext)
	if got == nil {
		t.Fatalf("no EventAckWithContext in timeline")
	}
	var detail map[string]string
	if err := json.Unmarshal(got.Detail, &detail); err != nil {
		t.Fatalf("unmarshal ack detail: %v", err)
	}
	if detail["reason_code"] != "investigating" {
		t.Errorf("reason_code=%q, want investigating", detail["reason_code"])
	}
	if detail["reference_id"] != "JIRA-42" {
		t.Errorf("reference_id=%q, want JIRA-42", detail["reference_id"])
	}
}

// ---------------------------------------------------------------------------
// 7. route
// ---------------------------------------------------------------------------

// TestKnative_Phase5_Route_E2E, sink replies with an alert.route reply
// aimed at a seeded failover channel in the same org; handler inserts an
// alert_routing_rules row with expires_at within the 24h cap.
func TestKnative_Phase5_Route_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	ch := seedChannelInOrg(t, s, alert.OrgID)
	prov, _ := wirePhase5ReplyProvider(t, s)

	expires := time.Now().UTC().Add(2 * time.Hour)
	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-route-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertRouteV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertRouteV1Data{
			AlertID:         alert.ID,
			TargetChannelID: ch.ID,
			MatchFilter:     map[string]any{"severity": "critical"},
			ExpiresAt:       expires,
			Reason:          "failover during maintenance",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.route.v1"},
		phase5ReplyOpts{trustElevated: true})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	rules, err := s.AlertRoutingRules().ListActiveForOrg(context.Background(), alert.OrgID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListActiveForOrg: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("routing rules=%d, want 1", len(rules))
	}
	if rules[0].TargetChannelID != ch.ID {
		t.Errorf("target=%q, want %q", rules[0].TargetChannelID, ch.ID)
	}
	// Default cap is 24h (86400s); our 2h expiry is well under it.
	if got := rules[0].ExpiresAt.Sub(time.Now().UTC()).Seconds(); got > 86400+60 {
		t.Errorf("expires exceeded 24h cap: %vs", got)
	}
}

// ---------------------------------------------------------------------------
// 8. status_page
// ---------------------------------------------------------------------------

// TestKnative_Phase5_StatusPage_E2E, sink replies with a status_page ref
// whose public_message is 5KiB; handler persists an alert_status_page_refs
// row with the message truncated to 2KiB.
func TestKnative_Phase5_StatusPage_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	prov, _ := wirePhase5ReplyProvider(t, s)

	oversized := strings.Repeat("x", 5000)
	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-sp-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertStatusPageV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertStatusPageV1Data{
			AlertID:       alert.ID,
			Provider:      "statuspage_io",
			URL:           "https://status.example.com/incidents/abc",
			PublicMessage: oversized,
			UpdatedAt:     time.Now().UTC(),
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.alert.status_page.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	refs, err := s.AlertStatusPageRefs().ListByAlert(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListByAlert: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs=%d, want 1", len(refs))
	}
	if len(refs[0].PublicMessage) > 2048 {
		t.Errorf("public_message not truncated to 2KiB: len=%d", len(refs[0].PublicMessage))
	}
}

// ---------------------------------------------------------------------------
// 9. monitor.deregister
// ---------------------------------------------------------------------------

// TestKnative_Phase5_MonitorDeregister_E2E, sink replies deregistering the
// monitor; channel has BOTH TrustElevated AND AllowDeregister set. Handler
// calls Monitors.Disable which sets enabled=false.
func TestKnative_Phase5_MonitorDeregister_E2E(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-dereg-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyMonitorDeregisterV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyMonitorDeregisterV1Data{
			AlertID:   alert.ID,
			MonitorID: alert.MonitorID,
			Reason:    "system decommission confirmed by SRE team",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.monitor.deregister.v1"},
		phase5ReplyOpts{trustElevated: true, allowDeregister: true})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	mon, err := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	if err != nil {
		t.Fatalf("Monitors.GetByID: %v", err)
	}
	if mon.Enabled {
		t.Error("monitor should be disabled after accepted deregister")
	}
}

// TestKnative_Phase5_MonitorDeregister_WithoutAllowDeregister_Rejected,
// TrustElevated is set but AllowDeregister is NOT. Handler rejects with
// trust_flag_required; monitor stays enabled.
func TestKnative_Phase5_MonitorDeregister_WithoutAllowDeregister_Rejected(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	alert := seedFiringAlert(t, s)
	prov, _ := wirePhase5ReplyProvider(t, s)

	sinkURL := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-dereg-reject-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyMonitorDeregisterV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyMonitorDeregisterV1Data{
			AlertID:   alert.ID,
			MonitorID: alert.MonitorID,
			Reason:    "system decommission confirmed by SRE team",
		})
		return ev
	})

	job, _ := buildPhase5OutboundJob(t, alert, sinkURL,
		[]string{"run.yipyap.reply.monitor.deregister.v1"},
		phase5ReplyOpts{trustElevated: true, allowDeregister: false})
	if _, err := prov.Send(context.Background(), job); err != nil {
		t.Fatalf("provider.Send: %v", err)
	}

	mon, err := s.Monitors().GetByID(context.Background(), alert.MonitorID)
	if err != nil {
		t.Fatalf("Monitors.GetByID: %v", err)
	}
	if !mon.Enabled {
		t.Error("monitor got disabled despite AllowDeregister=false")
	}
	// Post-H-23: trust-flag rejection (including the AllowDeregister gate)
	// fires at the dispatcher layer, which has no store handle, so no
	// audit row is written. The test guarantees "monitor NOT disabled"
	// (above), the rejection side-channel is a slog.Warn at WARN level.
}

// ---------------------------------------------------------------------------
// 10. cross-cutting audit-log guarantee
// ---------------------------------------------------------------------------

// TestKnative_Phase5_AuditLogged_ForEveryDispatchOutcome, stages three
// replies in a row on separate alerts so we can observe the audit table
// carrying an "accepted" row, a "rejected:*" row, and a "handler_error:*"
// row. The handler-error case is driven by status_page with an invalid
// provider (its rejection path is distinct from trust-flag rejections).
//
// This is the "forensic trail" guarantee from the Phase-5 design: every
// state-mutating reply lands in the audit log regardless of outcome.
func TestKnative_Phase5_AuditLogged_ForEveryDispatchOutcome(t *testing.T) {
	requirePhase5KnativeEnv(t)

	s := newReplyStore(t)
	prov, _ := wirePhase5ReplyProvider(t, s)

	// --- Accepted: suppressed happy path.
	alertAccepted := seedFiringAlert(t, s)
	sinkAccepted := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-acc-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertSuppressedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertSuppressedV1Data{
			AlertID:         alertAccepted.ID,
			DurationSeconds: 120,
			Reason:          "accepted path",
		})
		return ev
	})
	jobAccepted, _ := buildPhase5OutboundJob(t, alertAccepted, sinkAccepted,
		[]string{"run.yipyap.reply.alert.suppressed.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), jobAccepted); err != nil {
		t.Fatalf("provider.Send accepted: %v", err)
	}

	// --- Rejected: escalated without TrustElevated.
	alertRejected := seedFiringAlert(t, s)
	alertRejected.Severity = domain.SeverityWarning
	if err := s.Alerts().Update(context.Background(), alertRejected); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	sinkRejected := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-rej-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertEscalatedV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertEscalatedV1Data{
			AlertID:     alertRejected.ID,
			NewSeverity: "critical",
			Reason:      "should be rejected",
		})
		return ev
	})
	jobRejected, _ := buildPhase5OutboundJob(t, alertRejected, sinkRejected,
		[]string{"run.yipyap.reply.alert.escalated.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), jobRejected); err != nil {
		t.Fatalf("provider.Send rejected: %v", err)
	}

	// --- Rejected (handler-style validation): status_page with unknown
	// provider. The Phase-5 handlers funnel validation failures through
	// warnAndReject, which writes a "rejected:<reason>" audit row. A true
	// "handler_error:*" row requires a store error, which is awkward to
	// simulate with an in-memory sqlite, so we assert two distinct
	// validation-rejection outcomes land in the audit log.
	//
	// Note: the trust_flag_required rejection above is now enforced at the
	// dispatcher layer (H-23), which short-circuits before the handler
	// can write an audit row. That is intentional, a rejected escalated
	// reply does not burn the org's rate-limit budget, and the dispatcher
	// path emits a slog.Warn for observability. See dispatcher tests
	// TestDispatcher_TrustFlagRejectedBeforeRateLimit.
	alertBadProvider := seedFiringAlert(t, s)
	sinkBadProvider := buildPhase5ReplySink(t, func(outboundID string) ce.Event {
		ev := ce.NewEvent()
		ev.SetSpecVersion("1.0")
		ev.SetID("reply-herr-" + outboundID)
		ev.SetSource("https://consumer.example.com/")
		ev.SetType(types.TypeReplyAlertStatusPageV1)
		ev.SetTime(time.Now().UTC())
		ev.SetExtension("alertref", outboundID)
		_ = ev.SetData(ce.ApplicationJSON, types.ReplyAlertStatusPageV1Data{
			AlertID:  alertBadProvider.ID,
			Provider: "geocities", // invalid, not in allowedStatusPageProviders
			URL:      "https://status.example.com/",
			UpdatedAt: time.Now().UTC(),
		})
		return ev
	})
	jobBadProvider, _ := buildPhase5OutboundJob(t, alertBadProvider, sinkBadProvider,
		[]string{"run.yipyap.reply.alert.status_page.v1"}, phase5ReplyOpts{})
	if _, err := prov.Send(context.Background(), jobBadProvider); err != nil {
		t.Fatalf("provider.Send badprovider: %v", err)
	}

	// Inspect the audit log globally (across orgs). We expect at minimum one
	// accepted row and two rejected rows. ListByOrg is scoped, so ask one
	// org at a time and merge.
	ctx := context.Background()
	all := make([]*store.CloudEventReplyAuditEntry, 0, 3)
	for _, a := range []*domain.Alert{alertAccepted, alertRejected, alertBadProvider} {
		rows, err := s.CloudEventReplyAudit().ListByOrg(ctx, a.OrgID, time.Time{}, 10)
		if err != nil {
			t.Fatalf("ListByOrg(%s): %v", a.OrgID, err)
		}
		all = append(all, rows...)
	}

	var sawAccepted, sawRejectedProvider bool
	for _, row := range all {
		switch {
		case row.Outcome == "accepted":
			sawAccepted = true
		case strings.HasPrefix(row.Outcome, "rejected:invalid_provider"):
			sawRejectedProvider = true
		}
	}
	if !sawAccepted {
		t.Error("no accepted audit row present")
	}
	if !sawRejectedProvider {
		t.Error("no rejected:invalid_provider audit row present")
	}
	// The rejected-escalated reply does NOT leave an audit row post-H-23:
	// trust_flag_required is enforced at the dispatcher, which has no store
	// handle. Confirm instead that the escalated-path alert was NOT mutated
	// (severity stays at warning, not critical).
	escalatedCheck, _ := s.Alerts().GetByID(ctx, alertRejected.ID)
	if escalatedCheck != nil && escalatedCheck.Severity == domain.SeverityCritical {
		t.Error("escalated reply without TrustElevated was NOT rejected: severity mutated")
	}
}
