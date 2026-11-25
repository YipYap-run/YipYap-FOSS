package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// checkMemberLimit applies the operator cap (YIPYAP_MAX_MEMBERS). No cap set
// means unlimited. Disabled users do not count, matching Users().ListByOrg.
func checkMemberLimit(ctx context.Context, s store.Store, org *domain.Org) error {
	capN := limits.Current().MaxMembers
	if capN <= 0 {
		return nil
	}
	rows, err := s.Users().ListByOrg(ctx, org.ID, capProbe(capN))
	if err != nil {
		return errCountMembers
	}
	if len(rows) > 0 {
		return &limits.CapError{Resource: "members", Max: capN, Env: limits.EnvMaxMembers}
	}
	return nil
}
