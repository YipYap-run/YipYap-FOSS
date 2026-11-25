package handlers

import (
	"context"
	"reflect"
)

// ChannelRecoveryNotifier tells the org owners that a channel they were
// emailed about is delivering again. *notify.ChannelAlerts satisfies it.
type ChannelRecoveryNotifier interface {
	ChannelRecovered(ctx context.Context, channelID string)
}

// SetChannelAlerts sets who to tell when a successful Test ends a failure
// streak the owners were emailed about. Without it, Test still clears the
// streak but sends no follow-up. A typed nil (the nil *notify.ChannelAlerts
// returned when no mailer is configured) counts as unset.
func (h *NotificationChannelHandler) SetChannelAlerts(n ChannelRecoveryNotifier) {
	if n != nil {
		if v := reflect.ValueOf(n); v.Kind() == reflect.Pointer && v.IsNil() {
			n = nil
		}
	}
	h.recovery = n
}

// PlatformEmail reports whether the server has a platform mailer.
func (h *NotificationChannelHandler) PlatformEmail() bool { return h.platformEmail }
