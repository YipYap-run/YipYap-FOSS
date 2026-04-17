package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type AlertHandler struct {
	store store.Store
}

func NewAlertHandler(s store.Store) *AlertHandler {
	return &AlertHandler{store: s}
}

type alertResponse struct {
	*domain.Alert
	MonitorName string `json:"monitor_name,omitempty"`
	MonitorType string `json:"monitor_type,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	RunbookURL  string `json:"runbook_url,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
}

func (h *AlertHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	q := r.URL.Query()
	filter := store.AlertFilter{
		ListParams: params,
		Status:     q.Get("status"),
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
	}

	jsonResponse(w, http.StatusOK, resp)
}

func (h *AlertHandler) Ack(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil || alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertAcknowledged
	alert.AcknowledgedAt = &now
	alert.AcknowledgedBy = claims.UserID

	if err := h.store.Alerts().Update(r.Context(), alert); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to acknowledge alert")
		return
	}

	event := &domain.AlertEvent{
		ID:           chi.URLParam(r, "id") + "-ack",
		AlertID:      id,
		EventType:    domain.EventAck,
		TargetUserID: claims.UserID,
		CreatedAt:    now,
	}
	_ = h.store.Alerts().CreateEvent(r.Context(), event)

	jsonResponse(w, http.StatusOK, alert)
}

func (h *AlertHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	alert, err := h.store.Alerts().GetByID(r.Context(), id)
	if err != nil || alert.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "alert not found")
		return
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now

	if err := h.store.Alerts().Update(r.Context(), alert); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to resolve alert")
		return
	}

	event := &domain.AlertEvent{
		ID:           id + "-resolve",
		AlertID:      id,
		EventType:    domain.EventResolved,
		TargetUserID: claims.UserID,
		CreatedAt:    now,
	}
	_ = h.store.Alerts().CreateEvent(r.Context(), event)

	jsonResponse(w, http.StatusOK, alert)
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
	jsonResponse(w, http.StatusOK, events)
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
