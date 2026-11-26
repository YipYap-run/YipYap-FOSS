package handlers

import "github.com/YipYap-run/YipYap-FOSS/internal/domain"

// Hooks for notification channel types that only some builds deliver.
// Build-tagged files fill these from init alongside their entries in
// channelConfigSchemas, secretFields and writeOnlyURLFields. Each map is
// keyed by channel type.
var (
	// channelCrossChecks validate rules that span several config fields.
	channelCrossChecks = map[string]func(cfg map[string]any) error{}

	// channelURLChecks apply SSRF protection to the control-plane URLs in
	// a channel config on create and update. They ignore a config they
	// cannot decode; validateChannelConfig reports that.
	channelURLChecks = map[string]func(config []byte) error{}

	// channelTestMessages build the message body of a test send. A non-nil
	// skip is answered with 200 as-is instead of sending, for a test the
	// channel would drop. An error is answered with a 500 carrying its text.
	channelTestMessages = map[string]func(orgID, config string, job *domain.NotificationJob) (skip map[string]any, err error){}
)
