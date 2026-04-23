package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func MFAEnforce(_ store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}
