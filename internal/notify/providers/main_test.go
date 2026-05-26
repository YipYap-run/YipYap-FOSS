package providers

import (
	"os"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// TestMain enables the control-plane allow-private toggle so the existing
// provider tests, which all use httptest.Server (127.0.0.1), reach the
// fake sinks. Production defaults remain strict (see H-2/H-3/H-4). The
// H-4 regression explicitly toggles this back to false while exercising
// its attack.
func TestMain(m *testing.M) {
	orig := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = true
	code := m.Run()
	checker.AllowPrivateControlPlaneTargets = orig
	os.Exit(code)
}
