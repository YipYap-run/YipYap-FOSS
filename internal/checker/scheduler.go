package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const (
	subjectCheckDue = "check.due"
	queueCheckers   = "checkers"
)

// AlertEvent is the payload published to the bus on alert trigger/recover.
type AlertEvent struct {
	OrgID       string             `json:"org_id"`
	MonitorID   string             `json:"monitor_id"`
	MonitorName string             `json:"monitor_name"`
	Status      domain.CheckStatus `json:"status"`
	LatencyMS   int                `json:"latency_ms"`
	StatusCode  int                `json:"status_code,omitempty"`
	Error       string             `json:"error,omitempty"`
	CheckedAt   time.Time          `json:"checked_at"`
}

// checkDueMsg is published by tickers and consumed by workers via queue group.
type checkDueMsg struct {
	MonitorID string `json:"monitor_id"`
}

// Scheduler manages running checks on intervals for each enabled monitor.
//
// In a multi-instance deployment, every instance runs tickers for all monitors
// (tickers are cheap  - just timers). When a tick fires, a check.due message is
// published to the bus. Workers compete via QueueSubscribe so only ONE instance
// executes the actual check. This avoids duplicate HTTP/TCP/DNS calls.
//
// Check execution is parallelised via a bounded worker pool (checkWorkers
// goroutines reading from checkCh). This prevents a large number of monitors
// from serialising through a single NATS callback and starving short-interval
// monitors.
type Scheduler struct {
	store    store.Store
	bus      bus.Bus
	cfg      CheckerConfig
	monitors map[string]*monitorRunner
	monMu    sync.RWMutex // protects monitors map (reads >> writes)

	// lastStatus tracks the last known status per monitor for state-change
	// detection. Separate mutex to avoid contention with the monitors map.
	lastStatus map[string]domain.CheckStatus
	statusMu   sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	priorityCh chan checkDueMsg // high-priority checks (short intervals)
	checkCh    chan checkDueMsg // regular-priority checks
	wg         sync.WaitGroup  // tracks worker goroutines
	bw         *batchWriter    // async check result writer
}

// CheckerConfig holds tunable parameters for the scheduler and batch writer.
// Zero values fall back to sensible defaults.
type CheckerConfig struct {
	Workers           int // worker pool goroutines (default 512)
	ChannelSize       int // worker dispatch channel buffer (default 8192)
	PriorityThreshold int // interval (seconds) at or below which monitors get priority (default 15)
	BatchSize         int // checks per DB flush batch (default 256)
	BatchWriters      int // concurrent drain→flush loops (default 4)
	FlushConcurrency  int // parallel DB writers per flush (default 32)
}

func (c CheckerConfig) workers() int           { return orDefault(c.Workers, 512) }
func (c CheckerConfig) channelSize() int       { return orDefault(c.ChannelSize, 8192) }
func (c CheckerConfig) priorityThreshold() int { return orDefault(c.PriorityThreshold, 15) }
func (c CheckerConfig) batchSize() int         { return orDefault(c.BatchSize, 256) }
func (c CheckerConfig) batchWriters() int      { return orDefault(c.BatchWriters, 4) }
func (c CheckerConfig) flushConcurrency() int  { return orDefault(c.FlushConcurrency, 32) }

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

type monitorRunner struct {
	monitor domain.Monitor
	ticker  *time.Ticker
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewScheduler creates a new Scheduler. Pass a zero-value CheckerConfig for defaults.
func NewScheduler(s store.Store, b bus.Bus, cfg CheckerConfig) *Scheduler {
	return &Scheduler{
		store:      s,
		bus:        b,
		cfg:        cfg,
		monitors:   make(map[string]*monitorRunner),
		lastStatus: make(map[string]domain.CheckStatus),
	}
}

// Start loads all enabled monitors, subscribes the check worker via a queue
// group, and launches per-monitor tickers.
func (s *Scheduler) Start(ctx context.Context) error {
	s.monMu.Lock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.monMu.Unlock()

	// Async batch writer: decouples DB write latency from check execution.
	s.bw = newBatchWriter(s.store.Checks(), s.cfg)

	// Worker pool: fan-out check execution across workers goroutines so
	// thousands of monitors don't serialise through a single callback.
	// Two channels implement priority scheduling: short-interval monitors
	// (≤ priorityThreshold) are dispatched to priorityCh and always
	// dequeued before regular checks.
	workers := s.cfg.workers()
	chanSize := s.cfg.channelSize()
	s.priorityCh = make(chan checkDueMsg, chanSize/2)
	s.checkCh = make(chan checkDueMsg, chanSize)
	for range workers {
		s.wg.Add(1)
		go s.checkWorker()
	}

	// Subscribe the check-execution worker. QueueSubscribe ensures that across
	// all scheduler instances only one worker handles each check.due message.
	// The callback dispatches to the worker pool via checkCh.
	if err := s.bus.QueueSubscribe(subjectCheckDue, queueCheckers, s.dispatchCheckDue); err != nil {
		return fmt.Errorf("subscribe check.due: %w", err)
	}

	monitors, err := s.store.Monitors().ListAllEnabled(ctx)
	if err != nil {
		return fmt.Errorf("load monitors: %w", err)
	}

	s.monMu.Lock()
	for _, m := range monitors {
		s.addMonitorLocked(m)
	}
	s.monMu.Unlock()
	slog.Info("scheduler started",
		"monitors", len(monitors),
		"workers", workers,
		"channel_size", chanSize,
		"priority_threshold_sec", s.cfg.priorityThreshold(),
	)

	// Periodic resync from DB to pick up new/changed/deleted monitors.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.resyncFromDB(s.ctx)
			}
		}
	}()


	return nil
}

// LoadOrgMonitors loads and starts all enabled monitors for a specific org.
func (s *Scheduler) LoadOrgMonitors(ctx context.Context, orgID string) error {
	enabled := true
	monitors, err := s.store.Monitors().ListByOrg(ctx, orgID, store.MonitorFilter{
		Enabled: &enabled,
		ListParams: store.ListParams{
			Limit: 10000,
		},
	})
	if err != nil {
		return err
	}

	s.monMu.Lock()
	defer s.monMu.Unlock()
	for _, m := range monitors {
		s.addMonitorLocked(m)
	}
	return nil
}

// Stop cancels all runners and waits for them to finish.
func (s *Scheduler) Stop() {
	s.monMu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	for id, runner := range s.monitors {
		runner.cancel()
		<-runner.done
		delete(s.monitors, id)
	}
	s.monMu.Unlock()

	// Drain worker pool, then flush remaining check results.
	if s.priorityCh != nil {
		close(s.priorityCh)
	}
	if s.checkCh != nil {
		close(s.checkCh)
	}
	s.wg.Wait()
	if s.bw != nil {
		s.bw.Stop()
	}
}

func (s *Scheduler) resyncFromDB(ctx context.Context) {
	monitors, err := s.store.Monitors().ListAllEnabled(ctx)
	if err != nil {
		slog.Error("scheduler: resync failed", "error", err)
		return
	}

	s.monMu.Lock()
	defer s.monMu.Unlock()

	// Track which monitors are still enabled in DB.
	active := make(map[string]bool, len(monitors))

	for _, m := range monitors {
		active[m.ID] = true
		if runner, ok := s.monitors[m.ID]; ok {
			// Existing monitor  - update if config changed.
			if runner.monitor.IntervalSeconds != m.IntervalSeconds ||
				runner.monitor.TimeoutSeconds != m.TimeoutSeconds {
				s.stopRunnerLocked(m.ID)
				s.addMonitorLocked(m)
			}
		} else {
			// New monitor  - start tracking it.
			s.addMonitorLocked(m)
		}
	}

	// Remove monitors that are no longer enabled or were deleted.
	for id := range s.monitors {
		if !active[id] {
			s.stopRunnerLocked(id)
		}
	}
}

// AddMonitor starts checking a monitor on its interval.
func (s *Scheduler) AddMonitor(m *domain.Monitor) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	s.addMonitorLocked(m)
}

func (s *Scheduler) stopRunnerLocked(id string) {
	if existing, ok := s.monitors[id]; ok {
		existing.cancel()
		<-existing.done
		delete(s.monitors, id)
	}
}

func (s *Scheduler) addMonitorLocked(m *domain.Monitor) {
	s.stopRunnerLocked(m.ID)

	if !m.Enabled {
		return
	}

	interval := time.Duration(m.IntervalSeconds) * time.Second
	if interval < time.Second {
		interval = time.Second
	}

	runnerCtx, runnerCancel := context.WithCancel(s.ctx)

	runner := &monitorRunner{
		monitor: *m,
		ticker:  time.NewTicker(interval),
		cancel:  runnerCancel,
		done:    make(chan struct{}),
	}

	s.monitors[m.ID] = runner
	go s.runTicker(runnerCtx, runner)
}

// RemoveMonitor stops checking a monitor.
func (s *Scheduler) RemoveMonitor(id string) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	s.stopRunnerLocked(id)
}

// UpdateMonitor replaces a running monitor with updated configuration.
func (s *Scheduler) UpdateMonitor(m *domain.Monitor) {
	s.monMu.Lock()
	defer s.monMu.Unlock()
	s.addMonitorLocked(m)
}

// runTicker fires check.due messages on each tick. The first tick is jittered
// by a random fraction of the interval to spread load across time and avoid a
// thundering herd when all tickers start simultaneously.
func (s *Scheduler) runTicker(ctx context.Context, runner *monitorRunner) {
	defer close(runner.done)
	defer runner.ticker.Stop()

	// Jitter the first tick: sleep for a random duration in [0, interval).
	interval := time.Duration(runner.monitor.IntervalSeconds) * time.Second
	jitter := time.Duration(rand.Int64N(int64(interval)))
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	// Fire the first check immediately after the jitter.
	msg := checkDueMsg{MonitorID: runner.monitor.ID}
	data, _ := json.Marshal(msg)
	if err := s.bus.Publish(ctx, subjectCheckDue, data); err != nil {
		slog.Error("failed to publish check.due", "monitor_id", runner.monitor.ID, "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-runner.ticker.C:
			msg := checkDueMsg{MonitorID: runner.monitor.ID}
			data, _ := json.Marshal(msg)
			if err := s.bus.Publish(ctx, subjectCheckDue, data); err != nil {
				slog.Error("failed to publish check.due", "monitor_id", runner.monitor.ID, "error", err)
			}
		}
	}
}

// dispatchCheckDue is the thin QueueSubscribe callback. It deserialises the
// message and routes it to either the priority or regular channel based on
// the monitor's interval. If the target channel is full the message is
// dropped (the next tick will retry).
func (s *Scheduler) dispatchCheckDue(_ context.Context, _ string, data []byte) error {
	var msg checkDueMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}

	// Determine target channel based on monitor interval.
	ch := s.checkCh
	s.monMu.RLock()
	if runner, ok := s.monitors[msg.MonitorID]; ok && runner.monitor.IntervalSeconds <= s.cfg.priorityThreshold() {
		ch = s.priorityCh
	}
	s.monMu.RUnlock()

	select {
	case ch <- msg:
	default:
		slog.Warn("check worker pool full, dropping check", "monitor_id", msg.MonitorID)
	}
	return nil
}

// checkWorker reads from both priority and regular channels, always
// preferring priority. This ensures short-interval monitors are never
// starved by bulk long-interval checks. The worker exits when the
// scheduler context is cancelled and both channels are drained.
func (s *Scheduler) checkWorker() {
	defer s.wg.Done()
	for {
		// Always drain the priority channel first.
		select {
		case msg, ok := <-s.priorityCh:
			if !ok {
				// Priority channel closed  - drain remaining regular work.
				for msg := range s.checkCh {
					s.runCheck(msg)
				}
				return
			}
			s.runCheck(msg)
			continue
		default:
		}

		// No priority work  - wait on either channel.
		select {
		case msg, ok := <-s.priorityCh:
			if !ok {
				for msg := range s.checkCh {
					s.runCheck(msg)
				}
				return
			}
			s.runCheck(msg)
		case msg, ok := <-s.checkCh:
			if !ok {
				// Regular channel closed  - drain remaining priority work.
				for msg := range s.priorityCh {
					s.runCheck(msg)
				}
				return
			}
			s.runCheck(msg)
		}
	}
}

func (s *Scheduler) runCheck(msg checkDueMsg) {
	s.monMu.RLock()
	runner, ok := s.monitors[msg.MonitorID]
	s.monMu.RUnlock()

	if !ok {
		m, err := s.store.Monitors().GetByID(s.ctx, msg.MonitorID)
		if err != nil || !m.Enabled {
			return
		}
		s.executeCheck(s.ctx, m)
		return
	}

	s.executeCheck(s.ctx, &runner.monitor)
}

func (s *Scheduler) executeCheck(ctx context.Context, m *domain.Monitor) {
	// Heartbeat monitors don't perform active checks  - they receive pings
	// via the API. The scheduler only enforces the grace period.
	if m.Type == domain.MonitorHeartbeat {
		s.executeHeartbeatCheck(ctx, m)
		return
	}

	chk := ForType(m.Type)
	if chk == nil {
		slog.Error("no checker for monitor type", "type", m.Type, "monitor_id", m.ID)
		return
	}

	timeout := time.Duration(m.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := chk.Check(checkCtx, m.Config)
	if err != nil {
		slog.Error("checker error", "monitor_id", m.ID, "error", err)
		result = &Result{
			Status: domain.StatusDown,
			Error:  err.Error(),
		}
	}

	// Evaluate latency thresholds  - upgrade a successful check to degraded if latency exceeds thresholds.
	if result.Status == domain.StatusUp {
		if m.LatencyCriticalMS > 0 && result.LatencyMS >= m.LatencyCriticalMS {
			result.Status = domain.StatusDegraded
			result.Error = fmt.Sprintf("latency %dms exceeds critical threshold %dms", result.LatencyMS, m.LatencyCriticalMS)
		} else if m.LatencyWarningMS > 0 && result.LatencyMS >= m.LatencyWarningMS {
			result.Status = domain.StatusDegraded
			result.Error = fmt.Sprintf("latency %dms exceeds warning threshold %dms", result.LatencyMS, m.LatencyWarningMS)
		}
	}

	// Evaluate match rules: load from store and apply first-match-wins logic.
	var matchedStateID string
	if result.StatusCode > 0 {
		if mrp, ok := s.store.(store.MonitorMatchRuleProvider); ok {
			rules, err := mrp.MonitorMatchRules().ListByMonitor(ctx, m.ID)
			if err == nil && len(rules) > 0 {
				domainRules := make([]domain.MonitorMatchRule, len(rules))
				for i, r := range rules {
					domainRules[i] = *r
				}
				stateID, healthClass := EvaluateMatchRules(domainRules, result.StatusCode, result.ResponseBody, result.ResponseHeaders)
				if stateID != "" {
					matchedStateID = stateID
					result.Status = HealthClassToStatus(healthClass)
				}
			}
		}
	}

	// DNS change detection: if the check succeeded and there is no explicit
	// Expected value, compare resolved records against the previous check.
	if m.Type == domain.MonitorDNS && result.Status == domain.StatusUp && result.Metadata != "" {
		s.detectDNSChange(ctx, m, result)
	}

	now := time.Now().UTC()
	check := &domain.MonitorCheck{
		ID:             uuid.New().String(),
		MonitorID:      m.ID,
		Status:         result.Status,
		LatencyMS:      result.LatencyMS,
		StatusCode:     result.StatusCode,
		Error:          result.Error,
		Metadata:       result.Metadata,
		TLSExpiry:      result.TLSExpiry,
		CheckedAt:      now,
		MatchedStateID: matchedStateID,
	}

	s.bw.Enqueue(check)

	// Detect state transitions and publish events. Lazily seed the
	// last-known status from the DB on first check.
	s.statusMu.Lock()
	prevStatus, seeded := s.lastStatus[m.ID]
	s.statusMu.Unlock()

	if !seeded {
		// DB lookup outside the lock to avoid serialising 4K+ first-checks.
		if latest, err := s.store.Checks().GetLatest(ctx, m.ID); err == nil && latest != nil {
			prevStatus = latest.Status
		}
	}

	s.statusMu.Lock()
	s.lastStatus[m.ID] = result.Status
	s.statusMu.Unlock()

	if (result.Status == domain.StatusDown || result.Status == domain.StatusDegraded) && prevStatus == domain.StatusUp {
		s.publishAlertEvent(ctx, "alert.trigger", *m, result, now)
	} else if result.Status == domain.StatusDown && prevStatus == domain.StatusDegraded {
		// Escalation from degraded to down
		s.publishAlertEvent(ctx, "alert.trigger", *m, result, now)
	} else if result.Status == domain.StatusUp && (prevStatus == domain.StatusDown || prevStatus == domain.StatusDegraded) {
		s.publishAlertEvent(ctx, "alert.recover", *m, result, now)
	}
}

// detectDNSChange compares the current DNS resolution against the previous
// check's metadata. If records changed (and the monitor has no explicit
// Expected value), the result is downgraded to DOWN so an alert fires.
func (s *Scheduler) detectDNSChange(ctx context.Context, m *domain.Monitor, result *Result) {
	// If the monitor has an explicit Expected value, validation already
	// happened in the checker  - skip change detection.
	var cfg domain.DNSCheckConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		return
	}
	if cfg.Expected != "" {
		return
	}

	latest, err := s.store.Checks().GetLatest(ctx, m.ID)
	if err != nil || latest == nil || latest.Metadata == "" {
		// First check or no previous metadata  - nothing to compare.
		return
	}

	// Compare normalised metadata strings directly; they are sorted JSON.
	if result.Metadata != latest.Metadata {
		var prev, cur DNSMetadata
		if json.Unmarshal([]byte(latest.Metadata), &prev) != nil {
			return
		}
		if json.Unmarshal([]byte(result.Metadata), &cur) != nil {
			return
		}
		result.Status = domain.StatusDown
		result.Error = fmt.Sprintf("DNS records changed: %v → %v", prev.Records, cur.Records)
	}
}

// executeHeartbeatCheck enforces the grace period for heartbeat monitors.
// If no heartbeat ping has been received within the grace period, the
// monitor is marked as DOWN.
//
// The evaluator reads only the latest STATUS=UP check to find the last
// actual ping - Down checks in the table are evaluator-produced state
// transitions and must not be treated as a ping timestamp, or the monitor
// would oscillate Up / Down on every tick.
//
// Evaluator writes a check only on a state transition. Heartbeat POST
// ingestion writes the Up checks. This keeps the check log semantically
// clean (one row per real event) and avoids pinning "latest ping" to an
// evaluator-generated row.
func (s *Scheduler) executeHeartbeatCheck(ctx context.Context, m *domain.Monitor) {
	var cfg domain.HeartbeatCheckConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		slog.Error("heartbeat checker: unmarshal config", "monitor_id", m.ID, "error", err)
		return
	}

	gracePeriod := time.Duration(cfg.GracePeriodSeconds) * time.Second
	if gracePeriod <= 0 {
		gracePeriod = 5 * time.Minute // sensible default
	}

	now := time.Now().UTC()

	// Find the most recent ACTUAL ping (status=up). Evaluator-produced
	// Down rows are ignored so they cannot reset the "time since last
	// ping" clock.
	lastPing, err := s.store.Checks().GetLatestByStatus(ctx, m.ID, domain.StatusUp)
	if err != nil {
		slog.Error("heartbeat checker: look up last ping", "monitor_id", m.ID, "error", err)
		return
	}

	var result *Result
	if lastPing == nil {
		result = &Result{
			Status: domain.StatusDown,
			Error:  "no heartbeat received",
		}
	} else if now.Sub(lastPing.CheckedAt) > gracePeriod {
		result = &Result{
			Status: domain.StatusDown,
			Error:  fmt.Sprintf("no heartbeat received in %s (last: %s)", gracePeriod, lastPing.CheckedAt.Format(time.RFC3339)),
		}
	} else {
		result = &Result{
			Status: domain.StatusUp,
		}
	}

	// State-change detection. Seed prevStatus from in-memory cache, or
	// from the most recent stored check of any status on cold start.
	s.statusMu.Lock()
	prevStatus, seeded := s.lastStatus[m.ID]
	s.statusMu.Unlock()
	if !seeded {
		if latestAny, err := s.store.Checks().GetLatest(ctx, m.ID); err == nil && latestAny != nil {
			prevStatus = latestAny.Status
		} else {
			prevStatus = result.Status // avoids a spurious first-tick transition
		}
	}

	s.statusMu.Lock()
	s.lastStatus[m.ID] = result.Status
	s.statusMu.Unlock()

	// Only write a check (and publish an alert event) on a real transition.
	// Up checks are written by the POST ingest handler; Down checks come
	// from us here, exactly once per outage.
	if prevStatus == result.Status {
		return
	}

	check := &domain.MonitorCheck{
		ID:        uuid.New().String(),
		MonitorID: m.ID,
		Status:    result.Status,
		Error:     result.Error,
		CheckedAt: now,
	}
	s.bw.Enqueue(check)

	if result.Status == domain.StatusDown && prevStatus == domain.StatusUp {
		s.publishAlertEvent(ctx, "alert.trigger", *m, result, now)
	} else if result.Status == domain.StatusUp && prevStatus == domain.StatusDown {
		s.publishAlertEvent(ctx, "alert.recover", *m, result, now)
	}
}

func (s *Scheduler) publishAlertEvent(ctx context.Context, subject string, m domain.Monitor, result *Result, checkedAt time.Time) {
	evt := AlertEvent{
		OrgID:       m.OrgID,
		MonitorID:   m.ID,
		MonitorName: m.Name,
		Status:      result.Status,
		LatencyMS:   result.LatencyMS,
		StatusCode:  result.StatusCode,
		Error:       result.Error,
		CheckedAt:   checkedAt,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("failed to marshal alert event", "error", err)
		return
	}

	if err := s.bus.Publish(ctx, subject, data); err != nil {
		slog.Error("failed to publish alert event", "subject", subject, "error", err)
	}
}
