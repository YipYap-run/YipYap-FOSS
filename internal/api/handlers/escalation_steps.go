package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// Limits on what one step save may contain. They keep a single request from
// writing an unbounded number of rows and keep wait values in a sane range.
const (
	maxEscalationSteps       = 50
	maxTargetsPerStep        = 50
	maxStepWaitSeconds       = 30 * 24 * 60 * 60
	maxStepRepeatCount       = 100
	maxStepRepeatIntervalSec = 30 * 24 * 60 * 60
)

// stepWithTargets is the wire shape of a step in policy reads and step saves.
type stepWithTargets struct {
	domain.EscalationStep
	Targets []domain.StepTarget `json:"targets"`
}

// UnmarshalJSON reads a step save. A target that leaves out "simultaneous"
// pages together with the target before it, as every target did before the
// option took effect, so API clients that never sent it keep that behavior.
func (s *stepWithTargets) UnmarshalJSON(b []byte) error {
	type wireTarget struct {
		domain.StepTarget
		Simultaneous *bool `json:"simultaneous"`
	}
	var w struct {
		domain.EscalationStep
		Targets []wireTarget `json:"targets"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	s.EscalationStep = w.EscalationStep
	s.Targets = make([]domain.StepTarget, len(w.Targets))
	for i, t := range w.Targets {
		t.StepTarget.Simultaneous = t.Simultaneous == nil || *t.Simultaneous
		s.Targets[i] = t.StepTarget
	}
	return nil
}

// fieldErrors maps a request path such as "steps[0].targets[1].target_id" to
// a message the UI can show next to that field.
type fieldErrors map[string]string

func (f fieldErrors) add(field, msg string) {
	if _, ok := f[field]; !ok {
		f[field] = msg
	}
}

// writeFieldErrors responds 400 with {"error": first, "field": path,
// "fields": {path: msg}}. "error" keeps clients that only read it working.
func writeFieldErrors(w http.ResponseWriter, order []string, errs fieldErrors) {
	first := order[0]
	jsonResponse(w, http.StatusBadRequest, map[string]any{
		"error":  errs[first],
		"field":  first,
		"fields": errs,
	})
}

// validateSteps checks a step save against the org. It returns the field
// errors and their paths in request order (so "error" names the first one).
func validateSteps(ctx context.Context, s store.Store, orgID string, steps []stepWithTargets) (fieldErrors, []string) {
	errs := fieldErrors{}
	var order []string
	add := func(field, msg string) {
		if _, ok := errs[field]; !ok {
			order = append(order, field)
		}
		errs.add(field, msg)
	}

	if len(steps) > maxEscalationSteps {
		add("steps", fmt.Sprintf("A policy can have at most %d steps", maxEscalationSteps))
		return errs, order
	}

	// Cache lookups: one policy often names the same channel or schedule
	// many times.
	channelOK := map[string]bool{}
	scheduleOK := map[string]bool{}
	userOK := map[string]bool{}
	teamOK := map[string]bool{}

	for i, st := range steps {
		p := fmt.Sprintf("steps[%d]", i)
		if st.WaitSeconds < 0 {
			add(p+".wait_seconds", "Delay cannot be negative")
		} else if st.WaitSeconds > maxStepWaitSeconds {
			add(p+".wait_seconds", "Delay cannot be longer than 30 days")
		}
		if st.RepeatCount < 0 {
			add(p+".repeat_count", "Repeat count cannot be negative")
		} else if st.RepeatCount > maxStepRepeatCount {
			add(p+".repeat_count", fmt.Sprintf("Repeat count cannot be more than %d", maxStepRepeatCount))
		}
		if st.RepeatIntervalSeconds < 0 {
			add(p+".repeat_interval_seconds", "Repeat interval cannot be negative")
		} else if st.RepeatIntervalSeconds > maxStepRepeatIntervalSec {
			add(p+".repeat_interval_seconds", "Repeat interval cannot be longer than 30 days")
		}
		if len(st.Targets) == 0 {
			add(p+".targets", "Add at least one target")
			continue
		}
		if len(st.Targets) > maxTargetsPerStep {
			add(p+".targets", fmt.Sprintf("A step can have at most %d targets", maxTargetsPerStep))
			continue
		}

		for j, t := range st.Targets {
			tp := fmt.Sprintf("%s.targets[%d]", p, j)
			switch t.TargetType {
			case domain.TargetOnCallPrimary, domain.TargetOnCallSecondary:
				if t.TargetID == "" {
					add(tp+".target_id", "Select an on-call schedule")
				} else if !lookup(scheduleOK, t.TargetID, func() bool { return scheduleInOrg(ctx, s, orgID, t.TargetID) }) {
					add(tp+".target_id", "On-call schedule not found")
				}
			case domain.TargetUser:
				if t.TargetID == "" {
					add(tp+".target_id", "Select a user")
				} else if !lookup(userOK, t.TargetID, func() bool {
					u, err := s.Users().GetByID(ctx, t.TargetID)
					return err == nil && u != nil && u.OrgID == orgID
				}) {
					add(tp+".target_id", "User not found")
				}
			case domain.TargetTeam:
				if t.TargetID == "" {
					add(tp+".target_id", "Select a team")
				} else if !lookup(teamOK, t.TargetID, func() bool {
					tm, err := s.Teams().GetByID(ctx, t.TargetID)
					return err == nil && tm != nil && tm.OrgID == orgID
				}) {
					add(tp+".target_id", "Team not found")
				}
			case domain.TargetChannel:
				if t.ChannelID == "" {
					add(tp+".channel_id", "Select a notification channel")
				}
			default:
				add(tp+".target_type", "Unknown target type")
			}

			if t.ChannelID != "" && !lookup(channelOK, t.ChannelID, func() bool {
				ch, err := s.NotificationChannels().GetByID(ctx, t.ChannelID)
				return err == nil && ch != nil && ch.OrgID == orgID
			}) {
				add(tp+".channel_id", "Notification channel not found")
			}
		}
	}
	return errs, order
}

func lookup(cache map[string]bool, id string, check func() bool) bool {
	if ok, seen := cache[id]; seen {
		return ok
	}
	ok := check()
	cache[id] = ok
	return ok
}

// scheduleInOrg reports whether the schedule exists and its team belongs to
// orgID. Schedules carry no org column; the team does.
func scheduleInOrg(ctx context.Context, s store.Store, orgID, scheduleID string) bool {
	sched, err := s.Schedules().GetByID(ctx, scheduleID)
	if err != nil || sched == nil {
		return false
	}
	team, err := s.Teams().GetByID(ctx, sched.TeamID)
	return err == nil && team != nil && team.OrgID == orgID
}

// assignStepIDs keeps client-supplied step and target ids only when they
// already belong to this policy and are not repeated in the request, and
// mints new ones otherwise. Keeping ids lets in-flight alerts stay on their
// current step across a save; refusing foreign ids stops a request from
// colliding with another policy's rows.
func assignStepIDs(policyID string, req []stepWithTargets, existingSteps, existingTargets map[string]bool) ([]domain.EscalationStep, map[string][]domain.StepTarget) {
	usedSteps := map[string]bool{}
	usedTargets := map[string]bool{}
	steps := make([]domain.EscalationStep, len(req))
	targets := make(map[string][]domain.StepTarget, len(req))
	for i, s := range req {
		st := s.EscalationStep
		if st.ID == "" || !existingSteps[st.ID] || usedSteps[st.ID] {
			st.ID = uuid.New().String()
		}
		usedSteps[st.ID] = true
		st.PolicyID = policyID
		st.Position = i
		steps[i] = st

		ts := make([]domain.StepTarget, len(s.Targets))
		for j, t := range s.Targets {
			if t.ID == "" || !existingTargets[t.ID] || usedTargets[t.ID] {
				t.ID = uuid.New().String()
			}
			usedTargets[t.ID] = true
			t.StepID = st.ID
			if t.TargetType == domain.TargetChannel {
				t.TargetID = ""
			}
			ts[j] = t
		}
		targets[st.ID] = ts
	}
	return steps, targets
}

// loadSteps returns the policy's steps with their targets.
func loadSteps(ctx context.Context, s store.Store, policyID string) ([]stepWithTargets, error) {
	steps, err := s.EscalationPolicies().GetSteps(ctx, policyID)
	if err != nil {
		return nil, err
	}
	out := make([]stepWithTargets, 0, len(steps))
	for _, st := range steps {
		targets, err := s.EscalationPolicies().GetTargets(ctx, st.ID)
		if err != nil {
			return nil, err
		}
		if targets == nil {
			targets = []domain.StepTarget{}
		}
		out = append(out, stepWithTargets{EscalationStep: st, Targets: targets})
	}
	return out, nil
}
