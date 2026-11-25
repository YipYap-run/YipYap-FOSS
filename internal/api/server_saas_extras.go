package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func registerSaaSPreAuthRoutes(_ chi.Router, _ *handlers.AuthHandler) {}

func saasAuthExtras() []func(http.Handler) http.Handler { return nil }

func registerSaaSAuthRoutes(_ chi.Router, _ store.Store) {}
