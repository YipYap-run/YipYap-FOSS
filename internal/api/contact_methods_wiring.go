package api

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// newContactMethodHandler builds the contact method handler from the server
// options, taking care not to hand it typed-nil mailer or envelope values.
func newContactMethodHandler(s store.Store, opt ServerOptions) *handlers.ContactMethodHandler {
	var codeMailer handlers.CodeMailer
	if opt.Mailer != nil {
		codeMailer = opt.Mailer
	}
	var cipher contactmethod.Cipher
	if opt.Envelope != nil {
		cipher = contactmethod.Cipher{Enc: opt.Envelope.Encrypt, Dec: opt.Envelope.Decrypt}
	}
	return handlers.NewContactMethodHandler(s, opt.TestSender, codeMailer, cipher)
}
