package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// checkDiscordInstall is a no-op in FOSS, which has no platform Discord app.
func (e *Engine) checkDiscordInstall(_ context.Context, _ *domain.NotificationChannel, _ string) bool {
	return true
}
