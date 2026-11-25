// Package promo is a no-op in the FOSS build, which has no promo plan.
package promo

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Mailer sends a plain-text email.
type Mailer interface {
	Send(to, subject, body string) error
}

// Start is a no-op in the FOSS build.
func Start(_ context.Context, _ store.Store, _ Mailer, _ string) {}
