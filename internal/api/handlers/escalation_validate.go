package handlers

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func validateEscalationPolicy(_ context.Context, _ store.Store, _ string, p *domain.EscalationPolicy) error {
	if p.Loop {
		return fmt.Errorf("escalation loops require Pro tier. Upgrade at /settings/billing")
	}
	return nil
}
