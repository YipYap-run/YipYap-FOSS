package notify

import (
	"context"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// Notifier sends notifications over a specific channel type (e.g. webhook,
// email, Slack). Implementations live in the providers sub-package.
type Notifier interface {
	// Channel returns the channel identifier (e.g. "webhook", "slack").
	Channel() string

	// Send delivers a single notification job. It returns an external
	// message ID on success (provider-specific) or an error.
	Send(ctx context.Context, job domain.NotificationJob) (string, error)

	// MaxConcurrency returns the maximum number of concurrent workers
	// that should send via this provider.
	MaxConcurrency() int
}

// DecryptFunc decrypts TargetConfig from a notification job.
// A nil DecryptFunc is a no-op (returns input unchanged).
type DecryptFunc func(ciphertext []byte) ([]byte, error)

// AckListener is optionally implemented by providers that can receive
// acknowledgement callbacks (e.g. interactive buttons in Slack/Discord).
type AckListener interface {
	ParseAck(r *http.Request) (*domain.AckEvent, error)
}
