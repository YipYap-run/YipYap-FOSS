package notify

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// StartPaidChannelNotices is a no-op in the FOSS build, which has no plans.
func StartPaidChannelNotices(_ context.Context, _ store.Store, _ Mailer, _ string) {}
