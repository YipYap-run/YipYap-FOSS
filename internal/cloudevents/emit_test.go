package cloudevents

import (
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestNewAlertFired(t *testing.T) {
	data := types.AlertFiredV1Data{
		AlertID:   "01HXY000000000000000000001",
		MonitorID: "01HXZ000000000000000000001",
		Severity:  "critical",
		Reason:    "503 from /health",
		FiredAt:   time.Now().UTC(),
	}
	ev, err := NewAlertFiredV1(OrgSource("org-abc"), data)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type() != types.TypeAlertFiredV1 {
		t.Errorf("type mismatch")
	}
	if ev.Source() != "https://console.yipyap.run/orgs/org-abc" {
		t.Errorf("source mismatch: %s", ev.Source())
	}
	if ev.Subject() != "alert/"+data.AlertID {
		t.Errorf("subject mismatch")
	}
	if err := ev.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewMonitorDown_SubjectAndSource(t *testing.T) {
	orgID := "org-abc"
	monitorID := "01HXZ000000000000000000002"
	source := MonitorSource(orgID, monitorID)
	wantSource := "https://console.yipyap.run/orgs/org-abc/monitors/" + monitorID
	if source != wantSource {
		t.Errorf("source mismatch: got %s want %s", source, wantSource)
	}
	data := types.MonitorDownV1Data{
		MonitorID:     monitorID,
		CheckID:       "01HXZ000000000000000000003",
		FailureReason: "timeout",
		CheckedAt:     time.Now().UTC(),
	}
	ev, err := NewMonitorDownV1(source, data)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Source() != wantSource {
		t.Errorf("event source mismatch: got %s want %s", ev.Source(), wantSource)
	}
	if ev.Subject() != "monitor/"+monitorID {
		t.Errorf("subject mismatch: got %s", ev.Subject())
	}
}

func TestAllConstructors_Validate(t *testing.T) {
	now := time.Now().UTC()
	alertID := "01HXY000000000000000000001"
	monitorID := "01HXZ000000000000000000001"
	checkID := "01HXZ000000000000000000002"
	windowID := "01HXW000000000000000000001"
	source := OrgSource("org-abc")

	cases := []struct {
		name     string
		wantType string
		build    func() (interface{ Type() string; Validate() error }, error)
	}{
		{
			name:     "alert.fired",
			wantType: types.TypeAlertFiredV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewAlertFiredV1(source, types.AlertFiredV1Data{
					AlertID: alertID, MonitorID: monitorID, Severity: "critical",
					Reason: "r", FiredAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "alert.acknowledged",
			wantType: types.TypeAlertAcknowledgedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewAlertAcknowledgedV1(source, types.AlertAcknowledgedV1Data{
					AlertID: alertID, MonitorID: monitorID, AckedBy: "user-1", AckedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "alert.resolved",
			wantType: types.TypeAlertResolvedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewAlertResolvedV1(source, types.AlertResolvedV1Data{
					AlertID: alertID, MonitorID: monitorID, ResolvedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "alert.escalated",
			wantType: types.TypeAlertEscalatedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewAlertEscalatedV1(source, types.AlertEscalatedV1Data{
					AlertID: alertID, MonitorID: monitorID, StepIndex: 1,
					ToChannel: "slack", EscalatedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "monitor.up",
			wantType: types.TypeMonitorUpV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMonitorUpV1(source, types.MonitorUpV1Data{
					MonitorID: monitorID, CheckID: checkID, CheckedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "monitor.down",
			wantType: types.TypeMonitorDownV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMonitorDownV1(source, types.MonitorDownV1Data{
					MonitorID: monitorID, CheckID: checkID, FailureReason: "timeout", CheckedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "monitor.degraded",
			wantType: types.TypeMonitorDegradedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMonitorDegradedV1(source, types.MonitorDegradedV1Data{
					MonitorID: monitorID, CheckID: checkID, Reason: "slow", CheckedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "monitor.heartbeat_missed",
			wantType: types.TypeMonitorHeartbeatMissedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMonitorHeartbeatMissedV1(source, types.MonitorHeartbeatMissedV1Data{
					MonitorID: monitorID, ExpectedBy: now,
				})
				return &ev, err
			},
		},
		{
			name:     "maintenance.started",
			wantType: types.TypeMaintenanceStartedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMaintenanceStartedV1(source, types.MaintenanceStartedV1Data{
					WindowID: windowID, Monitors: []string{monitorID}, StartedAt: now,
				})
				return &ev, err
			},
		},
		{
			name:     "maintenance.ended",
			wantType: types.TypeMaintenanceEndedV1,
			build: func() (interface{ Type() string; Validate() error }, error) {
				ev, err := NewMaintenanceEndedV1(source, types.MaintenanceEndedV1Data{
					WindowID: windowID, EndedAt: now,
				})
				return &ev, err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := tc.build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if ev.Type() != tc.wantType {
				t.Errorf("type mismatch: got %s want %s", ev.Type(), tc.wantType)
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("validate: %v", err)
			}
		})
	}
}

func TestNewEvent_IDsAreUnique(t *testing.T) {
	data := types.AlertFiredV1Data{
		AlertID: "01HXY000000000000000000001", MonitorID: "01HXZ000000000000000000001",
		Severity: "critical", Reason: "r", FiredAt: time.Now().UTC(),
	}
	a, err := NewAlertFiredV1(OrgSource("org-abc"), data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewAlertFiredV1(OrgSource("org-abc"), data)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() == b.ID() {
		t.Errorf("expected unique IDs, got %s twice", a.ID())
	}
}

func TestNewEvent_TimeIsUTC(t *testing.T) {
	data := types.AlertFiredV1Data{
		AlertID: "01HXY000000000000000000001", MonitorID: "01HXZ000000000000000000001",
		Severity: "critical", Reason: "r", FiredAt: time.Now().UTC(),
	}
	ev, err := NewAlertFiredV1(OrgSource("org-abc"), data)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Time().Location() != time.UTC {
		t.Errorf("expected UTC, got %s", ev.Time().Location())
	}
}
