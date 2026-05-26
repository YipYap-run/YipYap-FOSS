package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountJWKS is a no-op on FOSS builds. The self-hosted JWKS identity
// provider is SaaS-only.
func MountJWKS(_ chi.Router, _ http.Handler) {}
