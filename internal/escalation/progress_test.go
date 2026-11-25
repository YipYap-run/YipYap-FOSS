package escalation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func progressState(t *testing.T, stepID string, entered time.Time, retry int, meta escalationMeta) *domain.AlertEscalationState {
	t.Helper()
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	return &domain.AlertEscalationState{AlertID: "a", CurrentStepID: stepID, StepEnteredAt: entered, RetryCount: retry, NotificationsSent: data}
}

func TestDescribeProgress(t *testing.T) {
	entered := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	pos0, pos1 := 0, 1
	steps := []domain.EscalationStep{
		{ID: "s1", Position: 0, WaitSeconds: 300, RepeatCount: 1, RepeatIntervalSeconds: 60},
		{ID: "s2", Position: 1, WaitSeconds: 600},
	}
	two := func(string) []domain.StepTarget {
		return []domain.StepTarget{{ID: "t1"}, {ID: "t2"}} // sequential: two groups
	}
	one := func(string) []domain.StepTarget { return []domain.StepTarget{{ID: "t1"}} }
	maxLoops := 2

	cases := []struct {
		name       string
		status     domain.AlertStatus
		state      *domain.AlertEscalationState
		policy     *domain.EscalationPolicy
		targets    func(string) []domain.StepTarget
		wantState  string
		wantAction string
		wantAt     time.Duration // after entered; ignored when no action
		wantStep   int
	}{
		{"next group of a sequential step", domain.AlertFiring,
			progressState(t, "s1", entered, 0, escalationMeta{StepPosition: &pos0, Group: 1, PagedAt: &entered}),
			&domain.EscalationPolicy{}, two, ProgressActive, ActionPageNextTargets, 5 * time.Minute, 1},
		{"repeat uses the repeat interval", domain.AlertFiring,
			progressState(t, "s1", entered, 0, escalationMeta{StepPosition: &pos0, Group: 1, PagedAt: &entered}),
			&domain.EscalationPolicy{}, one, ProgressActive, ActionRepeatStep, time.Minute, 1},
		{"repeats done, move to step 2", domain.AlertFiring,
			progressState(t, "s1", entered, 1, escalationMeta{StepPosition: &pos0, Group: 1, PagedAt: &entered}),
			&domain.EscalationPolicy{}, one, ProgressActive, ActionNextStep, 5 * time.Minute, 1},
		{"last step of a looping policy restarts", domain.AlertFiring,
			progressState(t, "s2", entered, 0, escalationMeta{StepPosition: &pos1, Group: 1, PagedAt: &entered, LoopCount: 1}),
			&domain.EscalationPolicy{Loop: true, MaxLoops: &maxLoops}, one, ProgressActive, ActionRestartPolicy, 10 * time.Minute, 2},
		{"loops used up", domain.AlertFiring,
			progressState(t, "s2", entered, 0, escalationMeta{StepPosition: &pos1, Group: 1, PagedAt: &entered, LoopCount: 2}),
			&domain.EscalationPolicy{Loop: true, MaxLoops: &maxLoops}, one, ProgressExhausted, "", 0, 2},
		{"held by maintenance", domain.AlertFiring,
			progressState(t, "s1", entered, 0, escalationMeta{StepPosition: &pos0, Suppressed: true}),
			&domain.EscalationPolicy{}, one, ProgressHeld, "", 0, 1},
		{"stopped by a terminal step", domain.AlertFiring,
			progressState(t, "s1", entered, 0, escalationMeta{StepPosition: &pos0, Stopped: true}),
			&domain.EscalationPolicy{}, one, ProgressStopped, "", 0, 1},
		{"acknowledged", domain.AlertAcknowledged,
			progressState(t, "s1", entered, 0, escalationMeta{StepPosition: &pos0}),
			&domain.EscalationPolicy{}, one, ProgressAcknowledged, "", 0, 1},
		{"replaced step remaps by position", domain.AlertFiring,
			progressState(t, "gone", entered, 1, escalationMeta{StepPosition: &pos1, Group: 1, PagedAt: &entered}),
			&domain.EscalationPolicy{}, one, ProgressExhausted, "", 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := DescribeProgress(tc.status, tc.state, tc.policy, steps, tc.targets)
			if !ok {
				t.Fatal("no progress")
			}
			if p.State != tc.wantState || p.NextAction != tc.wantAction || p.StepNumber != tc.wantStep {
				t.Fatalf("got state %q action %q step %d, want %q %q %d", p.State, p.NextAction, p.StepNumber, tc.wantState, tc.wantAction, tc.wantStep)
			}
			if tc.wantAction == "" {
				if p.NextActionAt != nil {
					t.Fatalf("next action at %v, want none", p.NextActionAt)
				}
				return
			}
			if p.NextActionAt == nil || !p.NextActionAt.Equal(entered.Add(tc.wantAt)) {
				t.Fatalf("next action at %v, want %v", p.NextActionAt, entered.Add(tc.wantAt))
			}
		})
	}

	if _, ok := DescribeProgress(domain.AlertFiring, nil, nil, steps, one); ok {
		t.Fatal("nil state should report no progress")
	}
}

// The engine and the API must agree: DescribeProgress's due time is when
// advanceAlert acts. Checked here on the stepGap rule both use.
func TestDescribeProgress_MatchesStepGap(t *testing.T) {
	entered := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	step := domain.EscalationStep{ID: "s1", WaitSeconds: 120, RepeatCount: 2, RepeatIntervalSeconds: 30}
	state := progressState(t, "s1", entered, 0, escalationMeta{Group: 1, PagedAt: &entered})
	p, _ := DescribeProgress(domain.AlertFiring, state, &domain.EscalationPolicy{}, []domain.EscalationStep{step},
		func(string) []domain.StepTarget { return []domain.StepTarget{{ID: "t"}} })
	want := entered.Add(stepGap(&step, state, 1, 1))
	if p.NextActionAt == nil || !p.NextActionAt.Equal(want) {
		t.Fatalf("progress says %v, engine gap says %v", p.NextActionAt, want)
	}
}
