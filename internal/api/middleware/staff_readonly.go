package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

func isStaffReadonly(_ *auth.Claims) bool { return false }

// IsStaffReadonly is always false: FOSS has no operator tokens.
func IsStaffReadonly(_ *auth.Claims) bool { return false }

// RejectStaffReadonly is a pass-through: FOSS has no staff tokens.
func RejectStaffReadonly() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}
