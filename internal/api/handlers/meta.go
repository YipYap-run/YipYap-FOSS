package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func MetaGet(registrationEnabled bool, s store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"edition":              "foss",
			"billing_enabled":      false,
			"registration_enabled": registrationEnabled,
			"retention_days":       30,
		})
	}
}
