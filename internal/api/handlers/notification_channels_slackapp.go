package handlers

import (
	"context"
	"errors"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// prepareSlackAppConfig rejects slack_app channels: the platform Slack app
// is a hosted service. Other types pass through unchanged.
func prepareSlackAppConfig(_ context.Context, _ store.Store, _, channelType, config string) (string, error) {
	if channelType == "slack_app" {
		return "", errors.New("the YipYap Slack app is not available on self-hosted servers; use a Slack bot token instead")
	}
	return config, nil
}
