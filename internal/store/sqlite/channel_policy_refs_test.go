package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// DeleteIfUnused refuses while a policy in the channel's org sends
// through it, ignores references from other orgs, and ListPolicyRefs
// groups step numbers per policy.
func TestChannelPolicyRefsAndGuardedDelete(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	otherOrg := createTestOrg(t, s)
	chs := s.NotificationChannels()
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: "slack", Name: "ops", Config: "{}", Enabled: true}
	if err := chs.Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	policy := func(orgID, name string, stepChans ...string) *domain.EscalationPolicy {
		p := &domain.EscalationPolicy{ID: uuid.NewString(), OrgID: orgID, Name: name}
		if err := s.EscalationPolicies().Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		var steps []domain.EscalationStep
		targets := map[string][]domain.StepTarget{}
		for i, c := range stepChans {
			st := domain.EscalationStep{ID: uuid.NewString(), PolicyID: p.ID, Position: i}
			steps = append(steps, st)
			targets[st.ID] = []domain.StepTarget{{ID: uuid.NewString(), StepID: st.ID, TargetType: domain.TargetChannel, ChannelID: c}}
		}
		if err := s.EscalationPolicies().ReplaceSteps(ctx, p.ID, steps, targets); err != nil {
			t.Fatal(err)
		}
		return p
	}
	policy(otherOrg.ID, "Foreign", ch.ID)
	p := policy(org.ID, "Main", ch.ID, "", ch.ID)

	refs, err := chs.ListPolicyRefs(ctx, org.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ChannelPolicyRef{{PolicyID: p.ID, PolicyName: "Main", Steps: []int{1, 3}}}
	if !reflect.DeepEqual(refs[ch.ID], want) || len(refs) != 1 {
		t.Fatalf("refs = %+v", refs)
	}
	if one, err := chs.ListPolicyRefs(ctx, org.ID, ch.ID); err != nil || !reflect.DeepEqual(one[ch.ID], want) {
		t.Fatalf("single refs = %+v, %v", one, err)
	}

	if found, deleted, err := chs.DeleteIfUnused(ctx, ch.ID); err != nil || !found || deleted {
		t.Fatalf("in use: found=%v deleted=%v err=%v", found, deleted, err)
	}
	if err := s.EscalationPolicies().ReplaceSteps(ctx, p.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if found, deleted, err := chs.DeleteIfUnused(ctx, ch.ID); err != nil || !found || !deleted {
		t.Fatalf("unused: found=%v deleted=%v err=%v", found, deleted, err)
	}
	if found, deleted, err := chs.DeleteIfUnused(ctx, ch.ID); err != nil || found || deleted {
		t.Fatalf("missing: found=%v deleted=%v err=%v", found, deleted, err)
	}
}
