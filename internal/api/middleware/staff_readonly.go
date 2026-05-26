package middleware

import "github.com/YipYap-run/YipYap-FOSS/internal/auth"

func isStaffReadonly(_ *auth.Claims) bool { return false }
