package handlers

import "context"

// ChannelLookup resolves a chat channel ID to the server it belongs to and
// its name. The platform Discord app sets one so a discord_app channel can
// only be saved in the server its install is for. Nil in self-hosted
// builds.
type ChannelLookup func(ctx context.Context, channelID string) (serverID, name string, err error)

// SetDiscordChannelLookup attaches the platform Discord app's lookup.
func (h *NotificationChannelHandler) SetDiscordChannelLookup(fn ChannelLookup) {
	h.discordLookup = fn
}
