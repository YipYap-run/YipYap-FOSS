package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// EventsHandler handles the inbound events API (POST /api/v1/events).
type EventsHandler struct {
	store store.Store
	bus   bus.Bus
}

// NewEventsHandler creates a new EventsHandler.
func NewEventsHandler(s store.Store, b bus.Bus) *EventsHandler {
	return &EventsHandler{store: s, bus: b}
}

// inboundEvent is the PagerDuty Events API v2-compatible request body.
type inboundEvent struct {
	RoutingKey  string        `json:"routing_key"`
	EventAction string        `json:"event_action"`
	DedupKey    string        `json:"dedup_key"`
	Payload     *eventPayload `json:"payload"`
}

type eventPayload struct {
	Summary       string          `json:"summary"`
	Severity      string          `json:"severity"`
	Source        string          `json:"source"`
	Component     string          `json:"component"`
	CustomDetails json.RawMessage `json:"custom_details"`
}

// alertTriggerEvent mirrors the JSON shape of checker.AlertEvent /
// escalation.AlertTriggerEvent without importing those packages.
type alertTriggerEvent struct {
	MonitorID   string             `json:"monitor_id"`
	MonitorName string             `json:"monitor_name"`
	Status      domain.CheckStatus `json:"status"`
	LatencyMS   int                `json:"latency_ms"`
	StatusCode  int                `json:"status_code,omitempty"`
	Error       string             `json:"error,omitempty"`
	CheckedAt   time.Time          `json:"checked_at"`
}

type eventResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	DedupKey string `json:"dedup_key"`
}

// Ingest handles POST /api/v1/events.
func (h *EventsHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	var req inboundEvent
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate routing_key.
	if req.RoutingKey == "" {
		errorResponse(w, http.StatusBadRequest, "routing_key is required")
		return
	}

	// Validate event_action.
	switch req.EventAction {
	case "trigger", "acknowledge", "resolve":
		// ok
	default:
		errorResponse(w, http.StatusBadRequest, "event_action must be one of: trigger, acknowledge, resolve")
		return
	}

	// Look up monitor by integration key.
	monitor, err := h.store.Monitors().GetByIntegrationKey(r.Context(), req.RoutingKey)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid routing_key")
		return
	}
	if !monitor.Enabled {
		errorResponse(w, http.StatusUnprocessableEntity, "monitor is disabled")
		return
	}

	switch req.EventAction {
	case "trigger":
		h.handleTrigger(w, r, &req, monitor)
	case "acknowledge":
		h.handleAcknowledge(w, r, &req, monitor)
	case "resolve":
		h.handleResolve(w, r, &req, monitor)
	}
}

func (h *EventsHandler) handleTrigger(w http.ResponseWriter, r *http.Request, req *inboundEvent, monitor *domain.Monitor) {
	// Validate payload on trigger.
	if req.Payload == nil {
		errorResponse(w, http.StatusBadRequest, "payload is required for trigger events")
		return
	}
	if req.Payload.Summary == "" {
		errorResponse(w, http.StatusBadRequest, "payload.summary is required")
		return
	}
	switch req.Payload.Severity {
	case "critical", "warning", "info":
		// ok
	default:
		errorResponse(w, http.StatusBadRequest, "payload.severity must be one of: critical, warning, info")
		return
	}

	// Auto-generate dedup_key if not provided.
	dedupKey := req.DedupKey
	if dedupKey == "" {
		dedupKey = uuid.New().String()
	}

	// Map severity to check status.
	status := domain.StatusDown
	switch req.Payload.Severity {
	case "info", "warning":
		status = domain.StatusDegraded
	}

	errMsg := req.Payload.Summary
	if req.Payload.Source != "" {
		errMsg = "[" + req.Payload.Source + "] " + req.Payload.Summary
	}

	evt := alertTriggerEvent{
		MonitorID:   monitor.ID,
		MonitorName: monitor.Name,
		Status:      status,
		LatencyMS:   0,
		Error:       errMsg,
		CheckedAt:   time.Now().UTC(),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	if err := h.bus.Publish(r.Context(), "alert.trigger", data); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	jsonResponse(w, http.StatusAccepted, eventResponse{
		Status:   "success",
		Message:  "Event processed successfully.",
		DedupKey: dedupKey,
	})
}

func (h *EventsHandler) handleAcknowledge(w http.ResponseWriter, r *http.Request, req *inboundEvent, monitor *domain.Monitor) {
	if req.DedupKey == "" {
		errorResponse(w, http.StatusBadRequest, "dedup_key is required for acknowledge events")
		return
	}

	// Find active alert for this monitor. The dedup_key is the alert ID.
	alert, err := h.store.Alerts().GetByID(r.Context(), req.DedupKey)
	if err != nil || alert.MonitorID != monitor.ID {
		// Fall back to searching by active alert on this monitor.
		alert, err = h.store.Alerts().GetActiveByMonitor(r.Context(), monitor.ID)
		if err != nil {
			errorResponse(w, http.StatusNotFound, "no active alert found")
			return
		}
	}

	if alert.Status == domain.AlertResolved {
		errorResponse(w, http.StatusConflict, "alert is already resolved")
		return
	}

	ackEvt := domain.AckEvent{
		AlertID: alert.ID,
		UserID:  "",
		Channel: "events_api",
	}

	data, err := json.Marshal(ackEvt)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	if err := h.bus.Publish(r.Context(), "alert.ack", data); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	jsonResponse(w, http.StatusAccepted, eventResponse{
		Status:   "success",
		Message:  "Event processed successfully.",
		DedupKey: alert.ID,
	})
}

func (h *EventsHandler) handleResolve(w http.ResponseWriter, r *http.Request, req *inboundEvent, monitor *domain.Monitor) {
	if req.DedupKey == "" {
		errorResponse(w, http.StatusBadRequest, "dedup_key is required for resolve events")
		return
	}

	// Find active alert for this monitor.
	alert, err := h.store.Alerts().GetByID(r.Context(), req.DedupKey)
	if err != nil || alert.MonitorID != monitor.ID {
		alert, err = h.store.Alerts().GetActiveByMonitor(r.Context(), monitor.ID)
		if err != nil {
			errorResponse(w, http.StatusNotFound, "no active alert found")
			return
		}
	}

	if alert.Status == domain.AlertResolved {
		errorResponse(w, http.StatusConflict, "alert is already resolved")
		return
	}

	evt := alertTriggerEvent{
		MonitorID:   monitor.ID,
		MonitorName: monitor.Name,
		Status:      domain.StatusUp,
		LatencyMS:   0,
		CheckedAt:   time.Now().UTC(),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	if err := h.bus.Publish(r.Context(), "alert.recover", data); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	jsonResponse(w, http.StatusAccepted, eventResponse{
		Status:   "success",
		Message:  "Event processed successfully.",
		DedupKey: alert.ID,
	})
}
