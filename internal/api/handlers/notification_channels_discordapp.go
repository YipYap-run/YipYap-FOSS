package handlers

import (
	"context"
	"errors"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// prepareDiscordAppConfig rejects discord_app channels: the platform
// Discord app is a hosted service. Other types pass through unchanged.
func prepareDiscordAppConfig(_ context.Context, _ store.Store, _ ChannelLookup, _, channelType, config string) (string, error) {
	if channelType == "discord_app" {
		return "", errors.New("the YipYap Discord app is not available on self-hosted servers; use a Discord bot token instead")
	}
	return config, nil
}
