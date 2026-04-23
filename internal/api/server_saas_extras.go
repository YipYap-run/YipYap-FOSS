package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
)

func registerSaaSPreAuthRoutes(_ chi.Router, _ *handlers.AuthHandler) {}

func applySaaSAuthExtras(_ chi.Router) {}
