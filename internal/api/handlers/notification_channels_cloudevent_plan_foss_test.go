package handlers_test

import (
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// ceBumpToProByEmail (foss build) is a no-op, FOSS does not gate
// cloudevent_http channels by plan (RequirePaidPlan is a build-tagged
// pass-through), so no plan promotion is required for the post-gate
// tests to succeed.
func ceBumpToProByEmail(_ *testing.T, _ *sqlite.SQLiteStore, _ string) {}
