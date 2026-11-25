package handlers

import (
	"context"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// checkChannelPlanGate is a no-op in the FOSS build.
func checkChannelPlanGate(_ *NotificationChannelHandler, _ http.ResponseWriter, _ *http.Request, _ string) bool {
	return true
}

// channelPlanGateError is a no-op in the FOSS build.
func channelPlanGateError(_ context.Context, _ store.Store, _, _ string) error {
	return nil
}

// channelPlanGateReason is a no-op in the FOSS build: there are no plans.
func channelPlanGateReason(_ context.Context, _ store.Store, _ string) string { return "" }

// checkChannelTestGate is a no-op in the FOSS build, which has no plans or
// quotas.
func checkChannelTestGate(_ *NotificationChannelHandler, _ http.ResponseWriter, _ *http.Request, _ *domain.NotificationChannel) bool {
	return true
}
