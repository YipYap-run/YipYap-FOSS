package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// FOSS has no SMS or voice channels, so there is nothing to verify.
func registerPhoneVerifyRoutes(_ chi.Router, _ store.Store, _ handlers.PhoneCodeSender) {}
