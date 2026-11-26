package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// registerEventingRoutes mounts nothing in this build.
func registerEventingRoutes(_ chi.Router, _ store.Store, _ bus.Bus, _, _ func(http.Handler) http.Handler, _ ServerOptions) {
}

// registerEventingAdminRoutes mounts nothing in this build.
func registerEventingAdminRoutes(_ chi.Router, _ store.Store) {}
