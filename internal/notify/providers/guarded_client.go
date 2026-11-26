package providers

import (
	"net/http"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// newSSRFGuardedHTTPClient returns the shared guarded client
// (checker.NewGuardedHTTPClient) under the control-plane toggle. Every
// connect is checked on the resolved peer IP, which defeats DNS rebinding
// between create-time validation and send-time (H-4), and redirects are
// not followed, so a target cannot bounce the request to 169.254.169.254.
func newSSRFGuardedHTTPClient(timeout time.Duration) *http.Client {
	return checker.NewGuardedHTTPClient(timeout, checker.ControlPlanePolicy)
}
