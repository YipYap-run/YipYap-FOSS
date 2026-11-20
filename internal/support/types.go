package support

import "context"

// Publisher is the subset of the message bus the notifier needs.
type Publisher interface {
	PublishDurable(ctx context.Context, subject string, data []byte, msgID string) error
}

// Mailer is the subset of the SMTP mailer the notifier needs.
type Mailer interface {
	Send(to, subject, body string) error
}
