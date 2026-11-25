package handlers

import (
	"context"
	"time"
)

// channelPlanStatus: the FOSS build has no plans, so every channel is ok.
func (h *NotificationChannelHandler) channelPlanStatus(_ context.Context, _ string) (channelPlanStatusFunc, error) {
	return func(string) (string, *time.Time) { return "ok", nil }, nil
}
