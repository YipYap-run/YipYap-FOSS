// Package seed populates the database with realistic development data.
// Gated by YIPYAP_DEV_SEED=true  - only intended for local development.
package seed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// generatePassword creates a random 16-character hex password.
func generatePassword() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return uuid.NewString()[:16]
	}
	return hex.EncodeToString(b)
}

// Run seeds the database with development data and prints credentials.
// It is idempotent: if the admin user already exists, it skips seeding.
func Run(ctx context.Context, db store.Store) error {
	// Idempotency check: skip if admin already exists.
	if _, err := db.Users().GetByEmail(ctx, "admin@example.com"); err == nil {
		log.Println("dev seed: data already present, skipping")
		return nil
	}

	log.Println("dev seed: populating database with mock data...")

	seedPassword := generatePassword()
	now := time.Now().UTC()

	// ── Org ──────────────────────────────────────────────────────────────

	org := &domain.Org{
		ID:        uuid.NewString(),
		Name:      "Demo Organization",
		Slug:      "demo",
		Plan:      seedOrgPlan,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Orgs().Create(ctx, org); err != nil {
		return fmt.Errorf("create org: %w", err)
	}

	// ── Users ────────────────────────────────────────────────────────────

	hash, err := auth.HashPassword(seedPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	admin := &domain.User{
		ID:           uuid.NewString(),
		OrgID:        org.ID,
		Email:        "admin@example.com",
		PasswordHash: hash,
		Role:         domain.RoleOwner,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	alice := &domain.User{
		ID:           uuid.NewString(),
		OrgID:        org.ID,
		Email:        "alice@example.com",
		PasswordHash: hash,
		Role:         domain.RoleAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	bob := &domain.User{
		ID:           uuid.NewString(),
		OrgID:        org.ID,
		Email:        "bob@example.com",
		PasswordHash: hash,
		Role:         domain.RoleMember,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	carol := &domain.User{
		ID:           uuid.NewString(),
		OrgID:        org.ID,
		Email:        "carol@example.com",
		PasswordHash: hash,
		Role:         domain.RoleViewer,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	for _, u := range []*domain.User{admin, alice, bob, carol} {
		if err := db.Users().Create(ctx, u); err != nil {
			return fmt.Errorf("create user %s: %w", u.Email, err)
		}
	}

	// ── Teams ────────────────────────────────────────────────────────────

	platformTeam := &domain.Team{ID: uuid.NewString(), OrgID: org.ID, Name: "Platform"}
	infraTeam := &domain.Team{ID: uuid.NewString(), OrgID: org.ID, Name: "Infrastructure"}

	for _, t := range []*domain.Team{platformTeam, infraTeam} {
		if err := db.Teams().Create(ctx, t); err != nil {
			return fmt.Errorf("create team %s: %w", t.Name, err)
		}
	}

	members := []*domain.TeamMember{
		{TeamID: platformTeam.ID, UserID: admin.ID, Position: 0},
		{TeamID: platformTeam.ID, UserID: alice.ID, Position: 1},
		{TeamID: platformTeam.ID, UserID: bob.ID, Position: 2},
		{TeamID: infraTeam.ID, UserID: alice.ID, Position: 0},
		{TeamID: infraTeam.ID, UserID: bob.ID, Position: 1},
	}
	for _, m := range members {
		if err := db.Teams().AddMember(ctx, m); err != nil {
			return fmt.Errorf("add team member: %w", err)
		}
	}

	// ── Schedules ────────────────────────────────────────────────────────

	platformSchedule := &domain.Schedule{
		ID:               uuid.NewString(),
		TeamID:           platformTeam.ID,
		RotationInterval: domain.RotationWeekly,
		HandoffTime:      "09:00",
		EffectiveFrom:    now.AddDate(0, -1, 0),
		Timezone:         "America/New_York",
	}

	infraSchedule := &domain.Schedule{
		ID:               uuid.NewString(),
		TeamID:           infraTeam.ID,
		RotationInterval: domain.RotationDaily,
		HandoffTime:      "08:00",
		EffectiveFrom:    now.AddDate(0, -1, 0),
		Timezone:         "UTC",
	}

	for _, s := range []*domain.Schedule{platformSchedule, infraSchedule} {
		if err := db.Schedules().Create(ctx, s); err != nil {
			return fmt.Errorf("create schedule: %w", err)
		}
	}

	// Schedule override: Alice covers this weekend.
	nextSat := now.AddDate(0, 0, int(time.Saturday-now.Weekday()+7)%7)
	override := &domain.ScheduleOverride{
		ID:         uuid.NewString(),
		ScheduleID: platformSchedule.ID,
		UserID:     alice.ID,
		StartAt:    time.Date(nextSat.Year(), nextSat.Month(), nextSat.Day(), 0, 0, 0, 0, time.UTC),
		EndAt:      time.Date(nextSat.Year(), nextSat.Month(), nextSat.Day()+2, 0, 0, 0, 0, time.UTC),
		Reason:     "Covering weekend on-call",
	}
	if err := db.Schedules().CreateOverride(ctx, override); err != nil {
		return fmt.Errorf("create schedule override: %w", err)
	}

	// ── Notification channels ────────────────────────────────────────────

	channels := []*domain.NotificationChannel{
		{
			ID:      uuid.NewString(),
			OrgID:   org.ID,
			Type:    "slack",
			Name:    "#alerts-critical",
			Config:  `{"webhook_url":"https://hooks.slack.example.com/T000/B000/xxxx"}`,
			Enabled: true,
		},
		{
			ID:      uuid.NewString(),
			OrgID:   org.ID,
			Type:    "discord",
			Name:    "Discord Ops",
			Config:  `{"webhook_url":"https://discord.com/api/webhooks/0000/xxxx"}`,
			Enabled: true,
		},
		{
			ID:      uuid.NewString(),
			OrgID:   org.ID,
			Type:    "webhook",
			Name:    "PagerDuty relay",
			Config:  `{"url":"https://events.pagerduty.example.com/v2/enqueue","method":"POST","headers":{"Authorization":"Token token=xxxx"}}`,
			Enabled: true,
		},
	}

	for _, ch := range channels {
		if err := db.NotificationChannels().Create(ctx, ch); err != nil {
			return fmt.Errorf("create notification channel %s: %w", ch.Name, err)
		}
	}

	// ── Escalation policies ──────────────────────────────────────────────

	criticalPolicy := &domain.EscalationPolicy{
		ID:    uuid.NewString(),
		OrgID: org.ID,
		Name:  "Critical - Page On-Call",
		Loop:  true,
	}
	if err := db.EscalationPolicies().Create(ctx, criticalPolicy); err != nil {
		return fmt.Errorf("create escalation policy: %w", err)
	}

	step1ID := uuid.NewString()
	step2ID := uuid.NewString()
	steps := []domain.EscalationStep{
		{ID: step1ID, PolicyID: criticalPolicy.ID, Position: 0, WaitSeconds: 300, RepeatCount: 2, RepeatIntervalSeconds: 120},
		{ID: step2ID, PolicyID: criticalPolicy.ID, Position: 1, WaitSeconds: 600, IsTerminal: true},
	}
	targets := map[string][]domain.StepTarget{
		step1ID: {
			{ID: uuid.NewString(), StepID: step1ID, TargetType: domain.TargetOnCallPrimary, ChannelID: channels[0].ID, Simultaneous: true},
		},
		step2ID: {
			{ID: uuid.NewString(), StepID: step2ID, TargetType: domain.TargetTeam, TargetID: infraTeam.ID, ChannelID: channels[0].ID, Simultaneous: true},
			{ID: uuid.NewString(), StepID: step2ID, TargetType: domain.TargetChannel, ChannelID: channels[2].ID, Simultaneous: true},
		},
	}
	if err := db.EscalationPolicies().ReplaceSteps(ctx, criticalPolicy.ID, steps, targets); err != nil {
		return fmt.Errorf("replace escalation steps: %w", err)
	}

	warningPolicy := &domain.EscalationPolicy{
		ID:    uuid.NewString(),
		OrgID: org.ID,
		Name:  "Warning - Slack Only",
	}
	if err := db.EscalationPolicies().Create(ctx, warningPolicy); err != nil {
		return fmt.Errorf("create escalation policy: %w", err)
	}

	warnStepID := uuid.NewString()
	if err := db.EscalationPolicies().ReplaceSteps(ctx, warningPolicy.ID,
		[]domain.EscalationStep{
			{ID: warnStepID, PolicyID: warningPolicy.ID, Position: 0, WaitSeconds: 600, IsTerminal: true},
		},
		map[string][]domain.StepTarget{
			warnStepID: {
				{ID: uuid.NewString(), StepID: warnStepID, TargetType: domain.TargetChannel, ChannelID: channels[0].ID},
			},
		},
	); err != nil {
		return fmt.Errorf("replace warning escalation steps: %w", err)
	}

	// ── Monitors ─────────────────────────────────────────────────────────

	httpCfg := func(url string, status int) json.RawMessage {
		b, _ := json.Marshal(domain.HTTPCheckConfig{Method: "GET", URL: url, ExpectedStatus: status})
		return b
	}

	dnsCfg := func(host, rtype string) json.RawMessage {
		b, _ := json.Marshal(domain.DNSCheckConfig{Hostname: host, RecordType: rtype})
		return b
	}

	tcpCfg := func(host string, port int) json.RawMessage {
		b, _ := json.Marshal(domain.TCPCheckConfig{Host: host, Port: port})
		return b
	}

	monitors := []*domain.Monitor{
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "API Health Check",
			Type: domain.MonitorHTTP, Config: httpCfg("https://httpbin.org/status/200", 200),
			IntervalSeconds: 30, TimeoutSeconds: 10,
			LatencyWarningMS: 500, LatencyCriticalMS: 2000,
			DownSeverity: domain.SeverityCritical, DegradedSeverity: domain.SeverityWarning,
			Regions: []string{"us-east-1", "eu-west-1"},
			EscalationPolicyID: criticalPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "JSON API",
			Type: domain.MonitorHTTP, Config: httpCfg("https://jsonplaceholder.typicode.com/posts/1", 200),
			IntervalSeconds: 60, TimeoutSeconds: 15,
			LatencyWarningMS: 1000, LatencyCriticalMS: 5000,
			DownSeverity: domain.SeverityWarning,
			Regions:            []string{"us-east-1"},
			EscalationPolicyID: warningPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "HTTP Status Endpoint",
			Type: domain.MonitorHTTP, Config: httpCfg("https://httpbin.org/get", 200),
			IntervalSeconds: 30, TimeoutSeconds: 10,
			LatencyWarningMS: 300, LatencyCriticalMS: 1500,
			DownSeverity: domain.SeverityCritical, DegradedSeverity: domain.SeverityWarning,
			Regions:            []string{"us-east-1", "eu-west-1", "ap-southeast-1"},
			EscalationPolicyID: criticalPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "Cloudflare Status",
			Type: domain.MonitorHTTP, Config: httpCfg("https://www.cloudflarestatus.com/api/v2/summary.json", 200),
			IntervalSeconds: 60, TimeoutSeconds: 10,
			LatencyWarningMS: 500, LatencyCriticalMS: 2000,
			DownSeverity: domain.SeverityWarning,
			Regions:            []string{"us-east-1"},
			EscalationPolicyID: warningPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "DNS Lookup",
			Type: domain.MonitorDNS, Config: dnsCfg("httpbin.org", "A"),
			IntervalSeconds: 300, TimeoutSeconds: 10,
			DownSeverity:       domain.SeverityWarning,
			EscalationPolicyID: warningPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "TLS Port Check",
			Type: domain.MonitorTCP, Config: tcpCfg("httpbin.org", 443),
			IntervalSeconds: 120, TimeoutSeconds: 10,
			DownSeverity:       domain.SeverityInfo,
			EscalationPolicyID: warningPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.NewString(), OrgID: org.ID, Name: "Heartbeat Ingest",
			Type: domain.MonitorHeartbeat, Config: mustJSON(domain.HeartbeatCheckConfig{GracePeriodSeconds: 120}),
			IntervalSeconds: 60, TimeoutSeconds: 10,
			DownSeverity: domain.SeverityCritical,
			EscalationPolicyID: criticalPolicy.ID, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		},
	}

	for _, m := range monitors {
		if err := db.Monitors().Create(ctx, m); err != nil {
			return fmt.Errorf("create monitor %s: %w", m.Name, err)
		}
	}

	// Labels.
	labelSets := map[int]map[string]string{
		0: {"service": "transit-api", "env": "production", "tier": "critical"},
		1: {"service": "blog", "env": "production"},
		2: {"service": "statuscheck", "env": "production", "tier": "critical"},
		3: {"service": "transit-api", "check": "dns"},
		4: {"service": "blog", "check": "smtp"},
		5: {"service": "statuscheck", "check": "heartbeat"},
	}
	for i, labels := range labelSets {
		if err := db.Monitors().SetLabels(ctx, monitors[i].ID, labels); err != nil {
			return fmt.Errorf("set labels for monitor %d: %w", i, err)
		}
	}

	// ── Monitor checks (recent history) ──────────────────────────────────

	for _, m := range monitors {
		for i := 0; i < 30; i++ {
			status := domain.StatusUp
			latency := 80 + (i*7)%120
			var statusCode int
			var errMsg string

			if m.Type == domain.MonitorHTTP {
				statusCode = 200
			}

			// Inject a few degraded / down checks for realism.
			if i == 10 {
				status = domain.StatusDegraded
				latency = 1500
			}
			if i == 20 && m.Name == "DigiDoggi - Stream" {
				status = domain.StatusDown
				latency = 0
				statusCode = 503
				errMsg = "upstream timeout"
			}

			check := &domain.MonitorCheck{
				ID:         uuid.NewString(),
				MonitorID:  m.ID,
				Status:     status,
				LatencyMS:  latency,
				StatusCode: statusCode,
				Error:      errMsg,
				CheckedAt:  now.Add(-time.Duration(30-i) * time.Duration(m.IntervalSeconds) * time.Second),
			}
			if err := db.Checks().Create(ctx, check); err != nil {
				return fmt.Errorf("create check: %w", err)
			}
		}
	}

	// ── Alerts ───────────────────────────────────────────────────────────

	// Firing alert on DigiDoggi.
	firingAlert := &domain.Alert{
		ID:        uuid.NewString(),
		MonitorID: monitors[2].ID,
		OrgID:     org.ID,
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now.Add(-15 * time.Minute),
	}
	if err := db.Alerts().Create(ctx, firingAlert); err != nil {
		return fmt.Errorf("create firing alert: %w", err)
	}

	firingEvents := []*domain.AlertEvent{
		{ID: uuid.NewString(), AlertID: firingAlert.ID, EventType: domain.EventTriggered, CreatedAt: firingAlert.StartedAt},
		{ID: uuid.NewString(), AlertID: firingAlert.ID, EventType: domain.EventNotified, Channel: "slack", TargetUserID: alice.ID, CreatedAt: firingAlert.StartedAt.Add(10 * time.Second)},
		{ID: uuid.NewString(), AlertID: firingAlert.ID, EventType: domain.EventEscalated, CreatedAt: firingAlert.StartedAt.Add(5 * time.Minute)},
	}
	for _, e := range firingEvents {
		if err := db.Alerts().CreateEvent(ctx, e); err != nil {
			return fmt.Errorf("create alert event: %w", err)
		}
	}

	if err := db.Alerts().UpsertEscalationState(ctx, &domain.AlertEscalationState{
		AlertID:       firingAlert.ID,
		CurrentStepID: step1ID,
		StepEnteredAt: firingAlert.StartedAt,
		RetryCount:    1,
	}); err != nil {
		return fmt.Errorf("upsert escalation state: %w", err)
	}

	// Resolved alert on Transit API (last week).
	resolvedAt := now.Add(-7 * 24 * time.Hour)
	ackAt := resolvedAt.Add(-20 * time.Minute)
	resolvedAlert := &domain.Alert{
		ID:             uuid.NewString(),
		MonitorID:      monitors[0].ID,
		OrgID:          org.ID,
		Status:         domain.AlertResolved,
		Severity:       domain.SeverityWarning,
		StartedAt:      resolvedAt.Add(-30 * time.Minute),
		AcknowledgedAt: &ackAt,
		AcknowledgedBy: admin.ID,
		ResolvedAt:     &resolvedAt,
	}
	if err := db.Alerts().Create(ctx, resolvedAlert); err != nil {
		return fmt.Errorf("create resolved alert: %w", err)
	}

	resolvedEvents := []*domain.AlertEvent{
		{ID: uuid.NewString(), AlertID: resolvedAlert.ID, EventType: domain.EventTriggered, CreatedAt: resolvedAlert.StartedAt},
		{ID: uuid.NewString(), AlertID: resolvedAlert.ID, EventType: domain.EventNotified, Channel: "slack", CreatedAt: resolvedAlert.StartedAt.Add(5 * time.Second)},
		{ID: uuid.NewString(), AlertID: resolvedAlert.ID, EventType: domain.EventAck, TargetUserID: admin.ID, CreatedAt: ackAt},
		{ID: uuid.NewString(), AlertID: resolvedAlert.ID, EventType: domain.EventResolved, CreatedAt: resolvedAt},
	}
	for _, e := range resolvedEvents {
		if err := db.Alerts().CreateEvent(ctx, e); err != nil {
			return fmt.Errorf("create alert event: %w", err)
		}
	}

	// Acknowledged alert on Blog.
	blogAckAt := now.Add(-2 * time.Hour)
	ackAlert := &domain.Alert{
		ID:             uuid.NewString(),
		MonitorID:      monitors[1].ID,
		OrgID:          org.ID,
		Status:         domain.AlertAcknowledged,
		Severity:       domain.SeverityWarning,
		StartedAt:      now.Add(-3 * time.Hour),
		AcknowledgedAt: &blogAckAt,
		AcknowledgedBy: bob.ID,
	}
	if err := db.Alerts().Create(ctx, ackAlert); err != nil {
		return fmt.Errorf("create acknowledged alert: %w", err)
	}

	// ── Maintenance windows ──────────────────────────────────────────────

	mw := &domain.MaintenanceWindow{
		ID:             uuid.NewString(),
		OrgID:          org.ID,
		MonitorID:      monitors[1].ID,
		Name:           "Blog CMS upgrade",
		Description:    "Upgrading CMS to v4. Expect brief downtime.",
		StartAt:        now.Add(24 * time.Hour),
		EndAt:          now.Add(26 * time.Hour),
		Public:         true,
		SuppressAlerts: true,
		CreatedAt:      now,
		CreatedBy:      admin.ID,
	}
	if err := db.MaintenanceWindows().Create(ctx, mw); err != nil {
		return fmt.Errorf("create maintenance window: %w", err)
	}

	pastMW := &domain.MaintenanceWindow{
		ID:             uuid.NewString(),
		OrgID:          org.ID,
		Name:           "Infra patching",
		Description:    "Monthly OS security patches.",
		StartAt:        now.Add(-48 * time.Hour),
		EndAt:          now.Add(-46 * time.Hour),
		Public:         false,
		SuppressAlerts: true,
		CreatedAt:      now.Add(-72 * time.Hour),
		CreatedBy:      alice.ID,
	}
	if err := db.MaintenanceWindows().Create(ctx, pastMW); err != nil {
		return fmt.Errorf("create past maintenance window: %w", err)
	}

	// ── Status page (non-FOSS only) ─────────────────────────────────────

	if err := seedStatusPages(ctx, db, org, monitors); err != nil {
		return err
	}

	// ── Service catalog (non-FOSS only) ──────────────────────────────────

	if err := seedServices(ctx, db, org, monitors); err != nil {
		return err
	}

	// ── Incidents (non-FOSS only) ─────────────────────────────────────────

	if err := seedIncidents(ctx, db, org, monitors, admin); err != nil {
		return err
	}

	// ── API key ──────────────────────────────────────────────────────────

	rawKey := "yy_" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	h := sha256.Sum256([]byte(rawKey))
	apiKey := &domain.APIKey{
		ID:        uuid.NewString(),
		OrgID:     org.ID,
		Name:      "CI/CD pipeline",
		KeyHash:   hex.EncodeToString(h[:]),
		Prefix:    rawKey[:11],
		Scopes:    []string{"monitors:read", "monitors:write", "checks:write"},
		CreatedBy: admin.ID,
		CreatedAt: now,
	}
	if err := db.APIKeys().Create(ctx, apiKey); err != nil {
		return fmt.Errorf("create api key: %w", err)
	}

	// ── Rollups ──────────────────────────────────────────────────────────

	for _, m := range monitors[:3] {
		// Hourly rollups - last 48 hours.
		for h := 47; h >= 0; h-- {
			hourStart := now.Truncate(time.Hour).Add(-time.Duration(h) * time.Hour)
			uptimePct := 99.5 + float64(h%5)*0.1
			if h == 12 {
				uptimePct = 95.0 // inject a blip
			}
			rollup := &domain.MonitorRollup{
				MonitorID:    m.ID,
				Period:       "hourly",
				PeriodStart:  hourStart,
				UptimePct:    uptimePct,
				AvgLatencyMS: 85.0 + float64(h%20)*3.5,
				P95LatencyMS: 160.0 + float64(h%15)*8.0,
				P99LatencyMS: 300.0 + float64(h%10)*20.0,
				CheckCount:   60,
				FailureCount: h % 7,
			}
			if err := db.Checks().CreateRollup(ctx, rollup); err != nil {
				return fmt.Errorf("create hourly rollup: %w", err)
			}
		}

		// Daily rollups - last 30 days.
		for d := 29; d >= 0; d-- {
			day := now.AddDate(0, 0, -d).Truncate(24 * time.Hour)
			uptimePct := 99.5 + float64(d%3)*0.15
			if d == 14 {
				uptimePct = 88.5 // simulate an incident day
			}
			rollup := &domain.MonitorRollup{
				MonitorID:    m.ID,
				Period:       "daily",
				PeriodStart:  day,
				UptimePct:    uptimePct,
				AvgLatencyMS: 95.0 + float64(d%10)*7.0,
				P95LatencyMS: 180.0 + float64(d%8)*15.0,
				P99LatencyMS: 350.0 + float64(d%6)*25.0,
				CheckCount:   1440,
				FailureCount: d % 4,
			}
			if err := db.Checks().CreateRollup(ctx, rollup); err != nil {
				return fmt.Errorf("create daily rollup: %w", err)
			}
		}
	}

	// ── Print credentials ────────────────────────────────────────────────

	fmt.Println()
	fmt.Println("══════════════════════════════════════════════════════")
	fmt.Println("  DEV SEED COMPLETE")
	fmt.Println("══════════════════════════════════════════════════════")
	fmt.Printf("  Admin email:    %s\n", admin.Email)
	fmt.Printf("  Password:       %s\n", seedPassword)
	fmt.Printf("  Org:            %s (slug: %s)\n", org.Name, org.Slug)
	fmt.Printf("  API key:        %s\n", rawKey)
	fmt.Println()
	fmt.Printf("  Other users (same password):\n")
	fmt.Printf("    %s  (admin)\n", alice.Email)
	fmt.Printf("    %s    (member)\n", bob.Email)
	fmt.Printf("    %s   (viewer)\n", carol.Email)
	fmt.Println("══════════════════════════════════════════════════════")
	fmt.Println()

	return nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
