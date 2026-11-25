package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type AlertHandler struct {
	store   store.Store
	emitter cloudevents.Emitter
	bus     bus.Bus
	cipher  contactmethod.Cipher
}

func (h *AlertHandler) SetContactMethodCipher(c contactmethod.Cipher) { h.cipher = c }

// SetBus attaches the message bus. With a bus, Ack and Resolve publish an
// alert.changed hint so other open dashboards refresh. Nil disables it.
func (h *AlertHandler) SetBus(b bus.Bus) { h.bus = b }

// announce publishes the alert.changed hint. Best effort.
func (h *AlertHandler) announce(ctx context.Context, a *domain.Alert) {
	if h.bus == nil {
		return
	}
	if data, err := json.Marshal(domain.NewAlertChanged(a)); err == nil {
		_ = h.bus.Publish(ctx, domain.SubjectAlertChanged, data)
	}
}

// NewAlertHandler builds an AlertHandler without CloudEvent emission.
func NewAlertHandler(s store.Store) *AlertHandler {
	return &AlertHandler{store: s}
}

// NewAlertHandlerWithEmitter builds an AlertHandler that emits CloudEvents
// for Ack and Resolve state transitions. Pass nil for em to disable emission.
func NewAlertHandlerWithEmitter(s store.Store, em cloudevents.Emitter) *AlertHandler {
	return &AlertHandler{store: s, emitter: em}
}

type alertResponse struct {
	*domain.Alert
	MonitorName string `json:"monitor_name,omitempty"`
	MonitorType string `json:"monitor_type,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	RunbookURL  string `json:"runbook_url,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	// AcknowledgedByName is the display name (or email) of the user in
	// acknowledged_by, so the detail page can say who acknowledged.
	AcknowledgedByName string `json:"acknowledged_by_name,omitempty"`
	*alertEscalationView
}

// alertEscalationView is the escalation status shown on the alert detail
// page. Get fills it from the monitor's current policy and the alert's
// escalation state; List leaves it out.
type alertEscalationView struct {
	EscalationPolicyID   string `json:"escalation_policy_id"`
	EscalationPolicyName string `json:"escalation_policy_name,omitempty"`
	// EscalationState is one of active, held, stopped, exhausted,
	// acknowledged, resolved, or none (the alert has no escalation state,
	// for example the policy was attached after the alert opened).
	EscalationState string `json:"escalation_state"`
	// CurrentStep is the 1-based step number, CurrentStepPosition the
	// stored position, StepCount the number of steps in the policy.
	CurrentStep         int `json:"current_step,omitempty"`
	CurrentStepPosition int `json:"current_step_position"`
	StepCount           int `json:"step_count"`
	// NextEscalationAction is page_next_targets, repeat_step, next_step,
	// restart_policy or stop. It and NextEscalationAt are set only while
	// the state is active.
	NextEscalationAction string     `json:"next_escalation_action,omitempty"`
	NextEscalationAt     *time.Time `json:"next_escalation_at,omitempty"`
	// NextPageAt is the earliest delayed notification rule still waiting.
	NextPageAt      *time.Time `json:"next_page_at,omitempty"`
	EscalationRound int        `json:"escalation_repeat"`
	EscalationLoop  int        `json:"escalation_loop"`
}

// alertListMaxLimit is the largest page the alerts list returns. It is
// above the usual 100 because the alerts page and the dashboard compute
// their rollups (open count, MTTA, MTTR, 7-day chart) from one page of 200
// and label them "last 200 alerts".
const alertListMaxLimit = 200

func (h *AlertHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	q := r.URL.Query()
	// paginationFromQuery resets anything above 100 to 50, which made a
	// limit=200 request quietly return 50. Allow up to alertListMaxLimit
	// here and clamp larger values to it instead.
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 100 {
		params.Limit = min(n, alertListMaxLimit)
	}
	statuses, err := parseAlertStatuses(q["status"])
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := store.AlertFilter{
		ListParams: params,
		Statuses:   statuses,
		Severity:   q.Get("severity"),
		MonitorID:  q.Get("monitor_id"),
	}
	alerts, err := h.store.Alerts().ListByOrg(r.Context(), claims.OrgID, filter)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list alerts")
		return
	}

	// Batch-fetch monitors for name, type, and endpoint (1 query).
	monitorIDs := make([]string, 0, len(alerts))
	seen := make(map[string]bool, len(alerts))
	for _, a := range alerts {
		if !seen[a.MonitorID] {
			monitorIDs = append(monitorIDs, a.MonitorID)
			seen[a.MonitorID] = true
		}
	}
	names, _ := h.store.Monitors().GetNamesByIDs(r.Context(), claims.OrgID, monitorIDs)

	result := make([]alertResponse, len(alerts))
	for i, a := range alerts {
		result[i] = alertResponse{
			Alert:       a,
			MonitorName: names[a.MonitorID],
		}
	}
	jsonResponse(w, http.StatusOK, result)
}

// parseAlertStatuses reads the status filter. It accepts the parameter more
// than once (status=firing&status=acknowledged), a comma list
// (status=firing,acknowledged), or both. Unknown values are rejected so a
// typo does not quietly return an empty list.
func parseAlertStatuses(values []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			st := strings.TrimSpace(part)
			if st == "" || seen[st] {
				continue
			}
			switch domain.AlertStatus(st) {
			case domain.AlertFiring, domain.AlertAcknowledged, domain.AlertResolved:
			default:
				return nil, fmt.Errorf("unknown alert status %q (use firing, acknowledged or resolved)", st)
			}
			seen[st] = true
			out = append(out, st)
		}
	}
	return out, nil
}

func (h *AlertHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}

	resp := alertResponse{Alert: alert}

	// Enrich with monitor details.
	if m, err := h.store.Monitors().GetByID(r.Context(), alert.MonitorID); err == nil {
		resp.MonitorName = m.Name
		resp.MonitorType = string(m.Type)
		resp.Endpoint = monitorEndpoint(m)
		resp.RunbookURL = m.RunbookURL
		resp.ServiceName = lookupServiceName(r.Context(), h.store, m.ServiceID)
		resp.alertEscalationView = h.escalationView(r.Context(), alert, m)
	}
	if alert.AcknowledgedBy != "" {
		if u, err := h.store.Users().GetByID(r.Context(), alert.AcknowledgedBy); err == nil && u.OrgID == alert.OrgID {
			resp.AcknowledgedByName = u.Name
			if resp.AcknowledgedByName == "" {
				resp.AcknowledgedByName = u.Email
			}
		}
	}

	jsonResponse(w, http.StatusOK, resp)
}

// escalationView reports where the alert stands in its monitor's policy,
// using the same step and timer rules as the escalation engine. It returns
// nil when the monitor has no policy or the policy is in another org.
func (h *AlertHandler) escalationView(ctx context.Context, alert *domain.Alert, m *domain.Monitor) *alertEscalationView {
	if m.EscalationPolicyID == "" {
		return nil
	}
	policy, err := h.store.EscalationPolicies().GetByID(ctx, m.EscalationPolicyID)
	if err != nil || policy == nil || policy.OrgID != alert.OrgID {
		return nil
	}
	view := &alertEscalationView{
		EscalationPolicyID:   policy.ID,
		EscalationPolicyName: policy.Name,
		EscalationState:      "none",
	}
	steps, err := h.store.EscalationPolicies().GetSteps(ctx, policy.ID)
	if err != nil {
		return view
	}
	view.StepCount = len(steps)
	state, err := h.store.Alerts().GetEscalationState(ctx, alert.ID)
	if err != nil || state == nil {
		switch alert.Status {
		case domain.AlertAcknowledged:
			view.EscalationState = escalation.ProgressAcknowledged
		case domain.AlertResolved:
			view.EscalationState = escalation.ProgressResolved
		}
		return view
	}
	loadTargets := func(stepID string) []domain.StepTarget {
		t, _ := h.store.EscalationPolicies().GetTargets(ctx, stepID)
		return t
	}
	p, ok := escalation.DescribeProgress(alert.Status, state, policy, steps, loadTargets)
	if !ok {
		return view
	}
	view.EscalationState = p.State
	view.CurrentStep = p.StepNumber
	view.CurrentStepPosition = p.StepPosition
	view.NextEscalationAction = p.NextAction
	view.NextEscalationAt = p.NextActionAt
	view.NextPageAt = p.NextPageAt
	view.EscalationRound = p.Repeat
	view.EscalationLoop = p.Loop
	return view
}

// Ack acknowledges a firing alert. Any other status answers 409 with who
// already moved it, so a stale tab cannot reopen a resolved alert (mon-10).
func (h *AlertHandler) Ack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil || alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}
	if alert.Status != domain.AlertFiring {
		h.writeStateConflict(r.Context(), w, alert)
		return
	}

	// Viewers are read-only except for acknowledging alerts paged to them.
	if !middleware.IsWriterRole(claims.Role) {
		paged, err := alertPagedToUser(r.Context(), h.store, alert.ID, claims.UserID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to check alert recipients")
			return
		}
		if !paged {
			errorResponse(w, http.StatusForbidden, "viewers can only acknowledge alerts paged to them")
			return
		}
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertAcknowledged
	alert.AcknowledgedAt = &now
	alert.AcknowledgedBy = claims.UserID

	// The status guard in the UPDATE closes the race with a resolve or ack
	// that lands between the read above and this write.
	if err := h.store.Alerts().UpdateIfStatus(r.Context(), alert, domain.AlertFiring); err != nil {
		h.writeUpdateError(r.Context(), w, id, err, "failed to acknowledge alert")
		return
	}

	event := &domain.AlertEvent{
		ID:           uuid.New().String(),
		AlertID:      id,
		EventType:    domain.EventAck,
		TargetUserID: claims.UserID,
		CreatedAt:    now,
	}
	_ = h.store.Alerts().CreateEvent(r.Context(), event)
	h.announce(r.Context(), alert)

	// Emit CloudEvent (no-op when no emitter is attached).
	_ = cloudevents.EmitAlertAcknowledged(r.Context(), h.emitter, alert.OrgID, types.AlertAcknowledgedV1Data{
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		AckedBy:   claims.UserID,
		AckedAt:   now,
	})

	jsonResponse(w, http.StatusOK, alert)
}

// Resolve closes a firing or acknowledged alert. While the monitor's latest
// check is still down or degraded it answers 409 and suggests Acknowledge or
// Mute, unless the org turned on allow_resolve_while_down: the checker opens
// alerts only on an up to down change, so resolving mid-outage would leave
// the rest of the outage unpaged (mon-10, core-monitoring#3).
func (h *AlertHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil || alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}
	if alert.Status == domain.AlertResolved {
		h.writeStateConflict(r.Context(), w, alert)
		return
	}
	if !h.allowResolveWhileDown(r.Context(), alert.OrgID) {
		failing, err := h.failingCheck(r.Context(), alert.MonitorID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to check monitor status")
			return
		}
		if failing != nil {
			writeMonitorDownConflict(w, alert, failing)
			return
		}
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now

	if err := h.store.Alerts().UpdateIfStatus(r.Context(), alert, domain.AlertFiring, domain.AlertAcknowledged); err != nil {
		h.writeUpdateError(r.Context(), w, id, err, "failed to resolve alert")
		return
	}
	escalation.SendResolves(r.Context(), h.store, h.bus, h.cipher, alert, now)
	_ = h.store.Alerts().DeleteEscalationState(r.Context(), alert.ID)

	event := &domain.AlertEvent{
		ID:           uuid.New().String(),
		AlertID:      id,
		EventType:    domain.EventResolved,
		TargetUserID: claims.UserID,
		CreatedAt:    now,
	}
	_ = h.store.Alerts().CreateEvent(r.Context(), event)
	h.announce(r.Context(), alert)

	// Emit CloudEvent (no-op when no emitter is attached).
	_ = cloudevents.EmitAlertResolved(r.Context(), h.emitter, alert.OrgID, types.AlertResolvedV1Data{
		AlertID:    alert.ID,
		MonitorID:  alert.MonitorID,
		ResolvedBy: claims.UserID,
		ResolvedAt: now,
	})

	// Check incident auto-resolve.
	h.maybeAutoResolveIncident(r.Context(), alert)

	jsonResponse(w, http.StatusOK, alert)
}

// writeUpdateError maps an UpdateIfStatus failure to a response: a status
// race becomes the same 409 a stale read would get.
func (h *AlertHandler) writeUpdateError(ctx context.Context, w http.ResponseWriter, id string, err error, msg string) {
	if errors.Is(err, store.ErrAlertStateChanged) {
		if cur, gerr := h.store.Alerts().GetByID(ctx, id); gerr == nil {
			h.writeStateConflict(ctx, w, cur)
			return
		}
	}
	errorResponse(w, http.StatusInternalServerError, msg)
}

func (h *AlertHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}
	events, err := h.store.Alerts().ListEvents(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list timeline")
		return
	}
	jsonResponse(w, http.StatusOK, h.labelTimeline(r, alert.OrgID, events))
}

// timelineEvent is an alert event plus display names, so the timeline can
// say who was paged and which channel got the message without the client
// loading every user and channel in the org.
type timelineEvent struct {
	*domain.AlertEvent
	TargetUserName string `json:"target_user_name,omitempty"`
	ChannelName    string `json:"channel_name,omitempty"`
}

// labelTimeline resolves the person and channel names on paging events.
// Names are looked up within the alert's org only, and a lookup that fails
// leaves the name empty.
func (h *AlertHandler) labelTimeline(r *http.Request, orgID string, events []*domain.AlertEvent) []timelineEvent {
	ctx := r.Context()
	users := map[string]string{}
	channels := map[string]string{}
	out := make([]timelineEvent, 0, len(events))
	for _, evt := range events {
		te := timelineEvent{AlertEvent: evt}
		if id := evt.TargetUserID; id != "" {
			name, seen := users[id]
			if !seen {
				if u, err := h.store.Users().GetByID(ctx, id); err == nil && u != nil && u.OrgID == orgID {
					name = u.Name
					if name == "" {
						name = u.Email
					}
				}
				users[id] = name
			}
			te.TargetUserName = name
		}
		var detail struct {
			ChannelID string `json:"channel_id"`
		}
		if len(evt.Detail) > 0 && json.Unmarshal(evt.Detail, &detail) == nil && detail.ChannelID != "" {
			name, seen := channels[detail.ChannelID]
			if !seen {
				if ch, err := h.store.NotificationChannels().GetByID(ctx, detail.ChannelID); err == nil && ch != nil && ch.OrgID == orgID {
					name = ch.Name
				}
				channels[detail.ChannelID] = name
			}
			te.ChannelName = name
		}
		out = append(out, te)
	}
	return out
}

// monitorEndpoint extracts the human-readable target from a monitor's Config
// (e.g. URL for HTTP, host:port for TCP, hostname for DNS/Ping).
func monitorEndpoint(m *domain.Monitor) string {
	if m == nil || m.Config == nil {
		return ""
	}
	switch m.Type {
	case domain.MonitorHTTP:
		var cfg domain.HTTPCheckConfig
		if json.Unmarshal(m.Config, &cfg) == nil {
			return cfg.URL
		}
	case domain.MonitorTCP:
		var cfg domain.TCPCheckConfig
		if json.Unmarshal(m.Config, &cfg) == nil {
			return fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		}
	case domain.MonitorPing:
		var cfg domain.PingCheckConfig
		if json.Unmarshal(m.Config, &cfg) == nil {
			return cfg.Host
		}
	case domain.MonitorDNS:
		var cfg domain.DNSCheckConfig
		if json.Unmarshal(m.Config, &cfg) == nil {
			return cfg.Hostname
		}
	case domain.MonitorHeartbeat:
		return "" // do not expose the raw heartbeat token in API responses
	}
	return ""
}
