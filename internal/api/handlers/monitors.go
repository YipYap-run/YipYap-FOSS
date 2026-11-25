package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

	// Latest check, recent checks and labels are each one batched query, so
	// the query count does not grow with the number of monitors (mon-18).
	ids := make([]string, len(monitors))
	for i, m := range monitors {
		ids[i] = m.ID
	}
	latestChecks, _ := h.store.Checks().GetLatestByMonitors(r.Context(), ids)

	// Last 20 checks per monitor for the uptime bar and percentage.
	recentChecksMap, err := h.store.Checks().ListRecentByMonitors(r.Context(), ids, 20)
	if err != nil {
		recentChecksMap = nil
	}

	if labelsByMonitor, err := h.store.Monitors().GetLabelsByMonitors(r.Context(), ids); err == nil {
		for _, m := range monitors {
			if labels := labelsByMonitor[m.ID]; len(labels) > 0 {
				m.Labels = labels
			}
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

	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}

	// Same checks and server-side fields as bulk import.
	if err := prepareNewMonitor(r.Context(), h.store, org, &m, time.Now().UTC().Truncate(time.Second)); err != nil {
		writeMonitorPrepareError(w, err)
		return
	}

	// Monitor limit check (plan-aware).
	if err := checkMonitorLimitN(r.Context(), h.store, claims.OrgID, org, 1); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
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

	var req monitorPatch
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := req.validate(m); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// SSRF protection: reject monitors targeting private/internal IPs.
	if req.hasConfig() {
		if err := validateMonitorTarget(m.Type, req.Config); err != nil {
			writeMonitorTargetError(w, err)
			return
		}
	}
	if req.RunbookURL != nil {
		if err := validateRunbookURL(*req.RunbookURL); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.IntervalSeconds != nil {
		org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to get org")
			return
		}
		if err := checkIntervalFloor(org, *req.IntervalSeconds); err != nil {
			errorResponse(w, http.StatusForbidden, err.Error())
			return
		}
	}

	// Validate only the references this request changes, so a monitor
	// whose policy or group was since deleted can still be edited.
	// Escalation policy, group and service must belong to the caller's
	// org (the service check is a no-op in FOSS).
	changed := req.changedRefs(m)
	if err := validateMonitorRefs(r.Context(), h.store, claims.OrgID, &changed); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	req.apply(m)
	m.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Monitors().Update(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	if req.Labels != nil {
		_ = h.store.Monitors().SetLabels(r.Context(), m.ID, *req.Labels)
		m.Labels = *req.Labels
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
	// An empty stored token must never authenticate: ConstantTimeCompare
	// of two empty strings returns 1.
	if m.HeartbeatToken == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(m.HeartbeatToken)) != 1 {
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
