package handlers

import "context"

// PhoneCodeSender delivers a phone verification code by SMS or voice. The
// SaaS build wires the platform Telnyx account; FOSS leaves it nil.
type PhoneCodeSender interface {
	SendCode(ctx context.Context, to, code, method string) error
}
