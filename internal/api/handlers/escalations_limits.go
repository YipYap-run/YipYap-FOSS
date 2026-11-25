package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// checkEscalationPolicyLimit applies the operator cap
// (YIPYAP_MAX_ESCALATION_POLICIES). No cap set means unlimited.
func checkEscalationPolicyLimit(ctx context.Context, s store.Store, orgID string) error {
	capN := limits.Current().MaxEscalationPolicies
	if capN <= 0 {
		return nil
	}
	rows, err := s.EscalationPolicies().ListByOrg(ctx, orgID, capProbe(capN))
	if err != nil {
		return fmt.Errorf("failed to count escalation policies")
	}
	if len(rows) > 0 {
		return &limits.CapError{Resource: "escalation policies", Max: capN, Env: limits.EnvMaxEscalationPolicies}
	}
	return nil
}

func checkEscalationPolicyLimitN(_ context.Context, _ store.Store, _ string, _ int) error {
	return nil
}
