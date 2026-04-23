package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// MonitorChangeKind describes what happened to a monitor.
type MonitorChangeKind int

const (
	MonitorCreated MonitorChangeKind = iota
	MonitorUpdated
	MonitorDeleted
)

type MonitorHandler struct {
	store    store.Store
	onChange func(kind MonitorChangeKind, monitorID string)

	// statsCache caches CheckStats responses per org to avoid repeated
	// expensive COUNT(*)+MIN/MAX scans.
	statsCache sync.Map // map[orgID]cachedStats
}

type cachedStats struct {
	data      map[string]interface{}
	expiresAt time.Time
}

func NewMonitorHandler(s store.Store) *MonitorHandler {
	return &MonitorHandler{store: s}
}

// SetOnChange registers a callback invoked after monitor create/update/delete.
func (h *MonitorHandler) SetOnChange(fn func(kind MonitorChangeKind, monitorID string)) {
	h.onChange = fn
}

func (h *MonitorHandler) notifyChange(kind MonitorChangeKind, monitorID string) {
	if h.onChange != nil {
		h.onChange(kind, monitorID)
	}
}

// monitorWithStatus wraps a Monitor with its latest check status.
type monitorWithStatus struct {
	*domain.Monitor
	Status       string      `json:"status"`
	LatencyMS    *int        `json:"latency_ms,omitempty"`
	Endpoint     string      `json:"endpoint,omitempty"`
	UptimePct    *float64    `json:"uptime_pct,omitempty"`
	RecentChecks []miniCheck `json:"recent_checks,omitempty"`
}

// miniCheck is a compact check summary for the monitors list.
type miniCheck struct {
	Status string `json:"status"`
}

func (h *MonitorHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	q := r.URL.Query()
	var enabled *bool
	if e := q.Get("enabled"); e != "" {
		v := e == "true"
		enabled = &v
	}
	filter := store.MonitorFilter{
		ListParams: params,
		Type:       q.Get("type"),
		Status:     q.Get("status"),
		Enabled:    enabled,
	}
	monitors, err := h.store.Monitors().ListByOrg(r.Context(), claims.OrgID, filter)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}

	// Batch-fetch latest check status for all monitors (1 query).
	ids := make([]string, len(monitors))
	for i, m := range monitors {
		ids[i] = m.ID
	}
	latestChecks, _ := h.store.Checks().GetLatestByMonitors(r.Context(), ids)

	// Fetch last 20 checks per monitor for uptime bar and percentage.
	recentChecksMap := make(map[string][]*domain.MonitorCheck, len(ids))
	for _, id := range ids {
		checks, err := h.store.Checks().ListByMonitor(r.Context(), id, store.CheckFilter{
			ListParams: store.ListParams{Limit: 20},
		})
		if err == nil {
			recentChecksMap[id] = checks
		}
	}

	// Batch-fetch labels for all monitors.
	for _, m := range monitors {
		labels, err := h.store.Monitors().GetLabels(r.Context(), m.ID)
		if err == nil && len(labels) > 0 {
			m.Labels = labels
		}
	}

	result := make([]monitorWithStatus, len(monitors))
	for i, m := range monitors {
		result[i] = monitorWithStatus{Monitor: m, Status: "unknown", Endpoint: monitorEndpoint(m)}
		if c, ok := latestChecks[m.ID]; ok {
			result[i].Status = string(c.Status)
			result[i].LatencyMS = &c.LatencyMS
		}
		if checks, ok := recentChecksMap[m.ID]; ok && len(checks) > 0 {
			upCount := 0
			mini := make([]miniCheck, len(checks))
			for j, c := range checks {
				mini[j] = miniCheck{Status: string(c.Status)}
				if c.Status == domain.StatusUp {
					upCount++
				}
			}
			result[i].RecentChecks = mini
			pct := float64(upCount) / float64(len(checks)) * 100
			result[i].UptimePct = &pct
		}
	}
	jsonResponse(w, http.StatusOK, result)
}

// monitorDetailResponse wraps a Monitor with a parsed Endpoint field.
type monitorDetailResponse struct {
	*domain.Monitor
	Endpoint    string     `json:"endpoint,omitempty"`
	Status      string     `json:"status,omitempty"`
	StatusSince *time.Time `json:"status_since,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	ServiceName string     `json:"service_name,omitempty"`
}

func (h *MonitorHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	// Enrich with labels.
	labels, err := h.store.Monitors().GetLabels(r.Context(), m.ID)
	if err == nil && len(labels) > 0 {
		m.Labels = labels
	}

	resp := monitorDetailResponse{
		Monitor:  m,
		Endpoint: monitorEndpoint(m),
	}

	// Include current status and when it started.
	if latest, err := h.store.Checks().GetLatest(r.Context(), id); err == nil && latest != nil {
		resp.Status = string(latest.Status)
		resp.LastError = latest.Error
		if since, err := h.store.Checks().GetStatusSince(r.Context(), id, latest.Status); err == nil {
			resp.StatusSince = since
		}
	}

	resp.ServiceName = lookupServiceName(r.Context(), h.store, m.ServiceID)

	jsonResponse(w, http.StatusOK, resp)
}

func (h *MonitorHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	var m domain.Monitor
	if err := decodeBody(r, &m); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// SSRF protection: reject monitors targeting private/internal IPs.
	if m.Config != nil {
		if err := validateMonitorTarget(m.Type, m.Config); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Reject dangerous URI schemes in runbook_url.
	if err := validateRunbookURL(m.RunbookURL); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Enforce tier limits.
	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}
	minInterval := domain.MinCheckIntervalSeconds(org.Plan)
	if m.IntervalSeconds == 0 {
		m.IntervalSeconds = minInterval
	}
	if m.IntervalSeconds < minInterval {
		errorResponse(w, http.StatusForbidden, fmt.Sprintf(
			"minimum check interval for your plan is %ds. Upgrade for faster checks", minInterval))
		return
	}

	// Monitor limit check (plan-aware).
	if err := checkMonitorLimit(r.Context(), h.store, claims.OrgID, org); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	m.ID = uuid.New().String()
	m.OrgID = claims.OrgID
	m.CreatedAt = now
	m.UpdatedAt = now

	if m.Type == domain.MonitorHeartbeat {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		m.HeartbeatToken = hex.EncodeToString(b)
	}

	// Check if org has mute_new_monitors enabled.
	if sp, ok := h.store.(store.OrgSettingsProvider); ok {
		if val, err := sp.OrgSettings().Get(r.Context(), claims.OrgID, "mute_new_monitors"); err == nil && val == "true" {
			m.Muted = true
		}
	}

	if err := h.store.Monitors().Create(r.Context(), &m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}
	if len(m.Labels) > 0 {
		_ = h.store.Monitors().SetLabels(r.Context(), m.ID, m.Labels)
	}
	h.notifyChange(MonitorCreated, m.ID)
	jsonResponse(w, http.StatusCreated, &m)
}

func (h *MonitorHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil || m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	var req domain.Monitor
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// SSRF protection: reject monitors targeting private/internal IPs.
	if req.Config != nil {
		if err := validateMonitorTarget(m.Type, req.Config); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := validateRunbookURL(req.RunbookURL); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name != "" {
		m.Name = req.Name
	}
	if req.Config != nil {
		m.Config = req.Config
	}
	if req.IntervalSeconds > 0 {
		org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
		if err == nil {
			minInterval := domain.MinCheckIntervalSeconds(org.Plan)
			if req.IntervalSeconds < minInterval {
				errorResponse(w, http.StatusForbidden, fmt.Sprintf(
					"minimum check interval for your plan is %ds. Upgrade for faster checks", minInterval))
				return
			}
		}
		m.IntervalSeconds = req.IntervalSeconds
	}
	if req.TimeoutSeconds > 0 {
		m.TimeoutSeconds = req.TimeoutSeconds
	}
	if req.EscalationPolicyID != "" {
		m.EscalationPolicyID = req.EscalationPolicyID
	}
	if req.IntegrationKey != "" {
		m.IntegrationKey = req.IntegrationKey
	}
	// group_id is always set (empty string clears it).
	m.GroupID = req.GroupID
	if len(req.Regions) > 0 {
		m.Regions = req.Regions
	}
	// Description is always set (empty string clears it).
	m.Description = req.Description
	m.AutoResolve = req.AutoResolve
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	if req.Labels != nil {
		_ = h.store.Monitors().SetLabels(r.Context(), m.ID, req.Labels)
		m.Labels = req.Labels
	}
	h.notifyChange(MonitorUpdated, m.ID)
	jsonResponse(w, http.StatusOK, m)
}

func (h *MonitorHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err := h.store.Monitors().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	h.notifyChange(MonitorDeleted, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *MonitorHandler) Pause(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil || m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	m.Enabled = false
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to pause monitor")
		return
	}
	h.notifyChange(MonitorUpdated, m.ID)
	jsonResponse(w, http.StatusOK, m)
}

func (h *MonitorHandler) Resume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil || m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	m.Enabled = true
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to resume monitor")
		return
	}
	h.notifyChange(MonitorUpdated, m.ID)
	jsonResponse(w, http.StatusOK, m)
}

func (h *MonitorHandler) Mute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil || m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	m.Muted = true
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to mute monitor")
		return
	}
	h.notifyChange(MonitorUpdated, m.ID)
	jsonResponse(w, http.StatusOK, m)
}

func (h *MonitorHandler) Unmute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil || m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	m.Muted = false
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to unmute monitor")
		return
	}
	h.notifyChange(MonitorUpdated, m.ID)
	jsonResponse(w, http.StatusOK, m)
}

func (h *MonitorHandler) ListChecks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	params := paginationFromQuery(r)
	filter := store.CheckFilter{ListParams: params}

	q := r.URL.Query()
	if s := q.Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.Since = &t
		}
	}
	if u := q.Get("until"); u != "" {
		if t, err := time.Parse(time.RFC3339, u); err == nil {
			filter.Until = &t
		}
	}
	if st := q.Get("status"); st != "" {
		switch domain.CheckStatus(st) {
		case domain.StatusUp, domain.StatusDown, domain.StatusDegraded:
			filter.Status = st
		default:
			errorResponse(w, http.StatusBadRequest, "invalid status: must be up, down, or degraded")
			return
		}
	}

	// Floor the time range to the org's retention window so COUNT(*)
	// never scans beyond retained data  - prevents full-table scans.
	if filter.Since == nil {
		org, err := h.store.Orgs().GetByID(r.Context(), m.OrgID)
		if err == nil {
			days := retentionDaysForPlan(org.Plan)
			floor := time.Now().UTC().AddDate(0, 0, -days)
			filter.Since = &floor
		}
	}

	checks, err := h.store.Checks().ListByMonitor(r.Context(), id, filter)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list checks")
		return
	}
	total, countErr := h.store.Checks().CountByMonitor(r.Context(), id, filter)
	if countErr != nil {
		slog.Error("check count failed", "monitor", id, "error", countErr)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"checks": checks,
		"total":  total,
	})
}

func (h *MonitorHandler) CheckStats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	// Serve from cache if fresh (60s TTL per org).
	if cached, ok := h.statsCache.Load(claims.OrgID); ok {
		cs := cached.(cachedStats)
		if time.Now().Before(cs.expiresAt) {
			jsonResponse(w, http.StatusOK, cs.data)
			return
		}
	}

	monitors, err := h.store.Monitors().ListByOrg(r.Context(), claims.OrgID, store.MonitorFilter{
		ListParams: store.ListParams{Limit: 1000},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	ids := make([]string, len(monitors))
	for i, m := range monitors {
		ids[i] = m.ID
	}
	agg, err := h.store.Checks().AggregateByOrg(r.Context(), ids)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to aggregate checks")
		return
	}
	data := map[string]interface{}{
		"monitor_count": len(monitors),
		"total_checks":  agg.TotalChecks,
		"oldest":        agg.Oldest,
		"newest":        agg.Newest,
	}
	h.statsCache.Store(claims.OrgID, cachedStats{
		data:      data,
		expiresAt: time.Now().Add(60 * time.Second),
	})
	jsonResponse(w, http.StatusOK, data)
}

type uptimeSlot struct {
	OK        bool    `json:"ok"`
	Status    string  `json:"status"`
	UptimePct float64 `json:"uptime_pct"`
}

func (h *MonitorHandler) Uptime(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	result := make(map[string][]uptimeSlot)

	// Try rollups first.
	hourly, _ := h.store.Checks().GetRollups(r.Context(), id, "hourly")
	daily, _ := h.store.Checks().GetRollups(r.Context(), id, "daily")

	if len(hourly) > 0 || len(daily) > 0 {
		if len(hourly) > 0 {
			slots := rollupSlots(hourly)
			if len(slots) > 24 {
				slots = slots[len(slots)-24:]
			}
			result["24h"] = slots
		}
		if len(daily) > 0 {
			slots := rollupSlots(daily)
			for _, kv := range []struct {
				key string
				n   int
			}{{"7d", 7}, {"30d", 30}, {"90d", 90}} {
				s := slots
				if len(s) > kv.n {
					s = s[len(s)-kv.n:]
				}
				result[kv.key] = s
			}
		}
	} else {
		// Fallback: compute uptime from raw checks.
		checks, err := h.store.Checks().ListByMonitor(r.Context(), id, store.CheckFilter{
			ListParams: store.ListParams{Limit: 200},
		})
		if err == nil && len(checks) > 0 {
			slots := make([]uptimeSlot, len(checks))
			for i, c := range checks {
				status := "up"
				switch c.Status {
				case domain.StatusDegraded:
					status = "degraded"
				case domain.StatusDown:
					status = "down"
				}
				slots[i] = uptimeSlot{
					OK:        c.Status == domain.StatusUp,
					Status:    status,
					UptimePct: 100,
				}
				if c.Status != domain.StatusUp {
					slots[i].UptimePct = 0
				}
			}
			// Use all checks for 24h view.
			result["24h"] = slots
		}
	}

	jsonResponse(w, http.StatusOK, result)
}

func rollupSlots(rollups []*domain.MonitorRollup) []uptimeSlot {
	slots := make([]uptimeSlot, len(rollups))
	for i, r := range rollups {
		status := "up"
		if r.UptimePct < 99.0 {
			status = "degraded"
		}
		if r.UptimePct < 90.0 {
			status = "down"
		}
		slots[i] = uptimeSlot{
			OK:        r.UptimePct >= 99.0,
			Status:    status,
			UptimePct: r.UptimePct,
		}
	}
	return slots
}

type latencyPoint struct {
	Time      time.Time `json:"time"`
	LatencyMS float64   `json:"latency_ms"`
	P95MS     float64   `json:"p95_ms"`
	P99MS     float64   `json:"p99_ms"`
}

func (h *MonitorHandler) Latency(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	// Try rollups first; fall back to raw checks if no rollups exist.
	rollups, err := h.store.Checks().GetRollups(r.Context(), id, "hourly")
	if err == nil && len(rollups) > 0 {
		points := make([]latencyPoint, len(rollups))
		for i, r := range rollups {
			points[i] = latencyPoint{
				Time:      r.PeriodStart,
				LatencyMS: r.AvgLatencyMS,
				P95MS:     r.P95LatencyMS,
				P99MS:     r.P99LatencyMS,
			}
		}
		jsonResponse(w, http.StatusOK, points)
		return
	}

	// Fallback: compute from raw checks.
	checks, err := h.store.Checks().ListByMonitor(r.Context(), id, store.CheckFilter{
		ListParams: store.ListParams{Limit: 100},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get latency data")
		return
	}
	points := make([]latencyPoint, len(checks))
	for i, c := range checks {
		points[i] = latencyPoint{
			Time:      c.CheckedAt,
			LatencyMS: float64(c.LatencyMS),
		}
	}
	jsonResponse(w, http.StatusOK, points)
}

func (h *MonitorHandler) SetLabels(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	var labels map[string]string
	if err := decodeBody(r, &labels); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.store.Monitors().SetLabels(r.Context(), id, labels); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to set labels")
		return
	}
	jsonResponse(w, http.StatusOK, labels)
}

func (h *MonitorHandler) DeleteLabel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.Monitors().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if m.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	key := chi.URLParam(r, "key")
	if err := h.store.Monitors().DeleteLabel(r.Context(), id, key); err != nil {
		errorResponse(w, http.StatusNotFound, "label not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MonitorHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	monitorID := chi.URLParam(r, "monitorID")

	m, err := h.store.Monitors().GetByID(r.Context(), monitorID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}
	if m.Type != domain.MonitorHeartbeat {
		errorResponse(w, http.StatusBadRequest, "not a heartbeat monitor")
		return
	}
	token := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(m.HeartbeatToken)) != 1 {
		errorResponse(w, http.StatusUnauthorized, "invalid heartbeat token")
		return
	}

	now := time.Now().UTC()

	// Capture the ping's source IP. trustedProxyRealIP middleware has
	// already resolved X-Forwarded-For / CF-Connecting-IP / X-Real-IP
	// into r.RemoteAddr when the direct peer is a trusted proxy, so we
	// only need to strip an optional port here.
	sourceIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(sourceIP); err == nil {
		sourceIP = host
	}
	meta, err := json.Marshal(map[string]string{"source_ip": sourceIP})
	if err != nil {
		meta = []byte(`{}`)
	}

	check := &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		Status:    domain.StatusUp,
		LatencyMS: 0,
		Metadata:  string(meta),
		CheckedAt: now,
	}
	if err := h.store.Checks().Create(r.Context(), check); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to record heartbeat")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// validateMonitorTarget performs SSRF validation for the monitor's target host(s)
// based on the monitor type. Returns an error if the target is private/internal.
func validateMonitorTarget(monitorType domain.MonitorType, config json.RawMessage) error {
	switch monitorType {
	case domain.MonitorHTTP:
		var cfg struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(config, &cfg) == nil && cfg.URL != "" {
			return checker.ValidateHTTPTarget(cfg.URL)
		}
	case domain.MonitorTCP:
		var cfg struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(config, &cfg) == nil && cfg.Host != "" {
			return checker.ValidateTarget(cfg.Host)
		}
	case domain.MonitorPing:
		var cfg struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(config, &cfg) == nil && cfg.Host != "" {
			return checker.ValidateTarget(cfg.Host)
		}
	case domain.MonitorDNS:
		var cfg struct {
			Hostname   string `json:"hostname"`
			Nameserver string `json:"nameserver,omitempty"`
		}
		if json.Unmarshal(config, &cfg) == nil {
			if cfg.Nameserver != "" {
				if err := checker.ValidateTarget(cfg.Nameserver); err != nil {
					return err
				}
			}
			if cfg.Hostname != "" {
				return checker.ValidateTarget(cfg.Hostname)
			}
		}
	}
	return nil
}

// validateRunbookURL rejects dangerous URI schemes (javascript:, data:, etc.)
// in runbook URLs. Empty URLs are allowed (field is optional).
func validateRunbookURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid runbook URL")
	}
	switch u.Scheme {
	case "https", "http", "":
		return nil
	default:
		return fmt.Errorf("runbook URL must use https:// or http://")
	}
}
