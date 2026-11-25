package handlers

import (
	"context"
	"log/slog"
	"time"
)

// channelPlanStatusFunc returns plan_status and grace_ends_at for a
// channel type in one org.
type channelPlanStatusFunc func(channelType string) (string, *time.Time)

// channelPlanStatusBestEffort is channelPlanStatus for responses to a
// write that already succeeded: a failed plan lookup leaves plan_status
// out instead of failing the request.
func (h *NotificationChannelHandler) channelPlanStatusBestEffort(ctx context.Context, orgID string) channelPlanStatusFunc {
	f, err := h.channelPlanStatus(ctx, orgID)
	if err != nil {
		slog.Warn("notification channel: read plan status", "org_id", orgID, "error", err)
		return nil
	}
	return f
}
