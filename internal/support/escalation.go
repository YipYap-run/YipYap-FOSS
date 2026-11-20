package support

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// StartEscalation is a no-op in the FOSS build, which has no support system.
func StartEscalation(_ context.Context, _ store.Store, _ Publisher, _ Mailer, _ string, _ *auth.JWTIssuer) {
}

// StartSurveys is a no-op in the FOSS build.
func StartSurveys(_ context.Context, _ store.Store, _ Publisher, _ Mailer, _ string, _ *auth.JWTIssuer) {
}
