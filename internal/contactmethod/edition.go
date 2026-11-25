package contactmethod

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// TypeSupported reports whether this build can deliver type t. The
// self-hosted edition has no platform SMS or voice provider.
func TypeSupported(t domain.ContactMethodType) bool {
	return t != domain.ContactSMS && t != domain.ContactVoice
}

func phoneDeliverable(string) error { return nil }

func phoneChannel(domain.ContactMethodType) (string, bool) { return "", false }

// PaidOnly is always false in the self-hosted edition.
func PaidOnly(domain.ContactMethodType) bool { return false }

// PlanError is a no-op in the self-hosted edition.
func PlanError(context.Context, store.Store, string, domain.ContactMethodType) error { return nil }
