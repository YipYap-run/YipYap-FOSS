package escalation

import (
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// Progress states, as returned in Progress.State.
const (
	// ProgressActive: the engine will act again at NextActionAt.
	ProgressActive = "active"
	// ProgressHeld: a maintenance window is holding the page.
	ProgressHeld = "held"
	// ProgressStopped: a step marked "stop escalation" has finished.
	ProgressStopped = "stopped"
	// ProgressExhausted: the last step finished and the policy does not
	// loop (or ran out of loops). Nobody else is paged.
	ProgressExhausted = "exhausted"
	// ProgressAcknowledged and ProgressResolved: the alert left the firing
	// state, so the engine no longer escalates it.
	ProgressAcknowledged = "acknowledged"
	ProgressResolved     = "resolved"
)

// Next actions, as returned in Progress.NextAction.
const (
	ActionPageNextTargets = "page_next_targets"
	ActionRepeatStep      = "repeat_step"
	ActionNextStep        = "next_step"
	ActionRestartPolicy   = "restart_policy"
	ActionStop            = "stop"
)

// Progress describes where an alert stands in its escalation policy. It is
// for display and has no side effects.
type Progress struct {
	// StepPosition is the stored position of the current step (0-based),
	// StepNumber its 1-based place in the policy, StepCount the number of
	// steps.
	StepPosition int
	StepNumber   int
	StepCount    int
	State        string
	// NextAction and NextActionAt are set only while State is active.
	NextAction   string
	NextActionAt *time.Time
	// Repeat is the round of the current step (0 for the first round) and
	// Loop the number of times the policy restarted.
	Repeat int
	Loop   int
	// NextPageAt is the earliest delayed notification rule still waiting
	// (for example "SMS after 5 minutes"). Nil when none is pending.
	NextPageAt *time.Time
}

// DescribeProgress works out the alert's escalation progress from its
// stored state with the same rules advanceAlert uses: steps sorted by
// position, the policy for its loop setting, and targets, which loads the
// targets of the current step. It returns false when the policy has no
// steps.
func DescribeProgress(status domain.AlertStatus, state *domain.AlertEscalationState, policy *domain.EscalationPolicy, steps []domain.EscalationStep, targets func(stepID string) []domain.StepTarget) (Progress, bool) {
	if state == nil || len(steps) == 0 {
		return Progress{}, false
	}
	meta := readMeta(state)

	idx := -1
	for i := range steps {
		if steps[i].ID == state.CurrentStepID {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Same remap as currentStep: the policy's steps were replaced.
		idx = 0
		if meta.StepPosition != nil {
			idx = len(steps) - 1
			for i := range steps {
				if steps[i].Position >= *meta.StepPosition {
					idx = i
					break
				}
			}
		}
	}
	step := &steps[idx]

	p := Progress{
		StepPosition: step.Position,
		StepNumber:   idx + 1,
		StepCount:    len(steps),
		Repeat:       state.RetryCount,
		Loop:         meta.LoopCount,
	}
	for _, pg := range meta.Pending {
		if p.NextPageAt == nil || pg.DueAt.Before(*p.NextPageAt) {
			due := pg.DueAt.UTC()
			p.NextPageAt = &due
		}
	}

	switch status {
	case domain.AlertAcknowledged:
		p.State = ProgressAcknowledged
		p.NextPageAt = nil
		return p, true
	case domain.AlertResolved:
		p.State = ProgressResolved
		p.NextPageAt = nil
		return p, true
	}
	if meta.Suppressed {
		p.State = ProgressHeld
		return p, true
	}
	if meta.Stopped {
		p.State = ProgressStopped
		return p, true
	}

	var stepTargets []domain.StepTarget
	if targets != nil {
		stepTargets = targets(step.ID)
	}
	groups := targetGroups(stepTargets)
	group := meta.Group
	pagedAt := state.StepEnteredAt
	if meta.PagedAt != nil {
		pagedAt = *meta.PagedAt
	} else {
		group = len(groups)
	}
	due := pagedAt.Add(stepGap(step, state, group, len(groups))).UTC()

	switch {
	case group < len(groups):
		p.NextAction = ActionPageNextTargets
	case state.RetryCount < step.RepeatCount:
		p.NextAction = ActionRepeatStep
	case step.IsTerminal:
		p.NextAction = ActionStop
	case idx+1 < len(steps):
		p.NextAction = ActionNextStep
	case policy != nil && policy.Loop && (policy.MaxLoops == nil || meta.LoopCount < *policy.MaxLoops):
		p.NextAction = ActionRestartPolicy
	default:
		p.State = ProgressExhausted
		return p, true
	}
	p.State = ProgressActive
	p.NextActionAt = &due
	return p, true
}
