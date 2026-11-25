package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// checkSlackInstall is a no-op in FOSS, which has no platform Slack app.
func (e *Engine) checkSlackInstall(_ context.Context, _ *domain.NotificationChannel, _ string) bool {
	return true
}
