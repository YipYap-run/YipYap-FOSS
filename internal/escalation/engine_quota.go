package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// reserveSMSVoice is a no-op in the FOSS build, which does not meter
// SMS/voice. Returns true (send).
func (e *Engine) reserveSMSVoice(_ context.Context, _ *domain.Alert, _ *domain.NotificationChannel) bool {
	return true
}

// checkChannelAllowed is a no-op in the FOSS build, which has no plans or
// quotas. It always lets the delivery go out.
func (e *Engine) checkChannelAllowed(_ context.Context, _ *domain.NotificationChannel, _ string) string {
	return ""
}
