package handlers_test

import (
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// bumpToProByEmail (foss build) is a no-op: FOSS has no plan gates
// (RequirePaidPlan is a build-tagged pass-through), so no plan promotion is
// required for the post-gate tests to succeed.
func bumpToProByEmail(_ *testing.T, _ *sqlite.SQLiteStore, _ string) {}
