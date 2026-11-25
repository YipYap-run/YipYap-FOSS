package escalation

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// checkPhoneVerified is a no-op in FOSS, which has no SMS or voice channels.
func (e *Engine) checkPhoneVerified(_ context.Context, _ *domain.NotificationChannel, _ string) bool {
	return true
}
