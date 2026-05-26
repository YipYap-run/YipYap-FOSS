package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func (e *Engine) enrichJobWithServiceContext(_ context.Context, _ *domain.NotificationJob, _ *domain.Monitor, _ *domain.Alert) {
	// FOSS: no service catalog enrichment
}
