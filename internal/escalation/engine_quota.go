package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// checkSMSVoiceQuota is a no-op in the FOSS build. Returns true (proceed).
func (e *Engine) checkSMSVoiceQuota(_ context.Context, _ *domain.NotificationChannel, _ string) bool {
	return true
}
