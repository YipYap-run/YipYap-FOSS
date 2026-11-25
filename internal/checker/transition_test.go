package checker

import (
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestAlertTransition(t *testing.T) {
	const (
		up   = domain.StatusUp
		down = domain.StatusDown
		deg  = domain.StatusDegraded
		none = domain.CheckStatus("")
	)
	cases := []struct {
		prev, cur domain.CheckStatus
		want      string
	}{
		{none, down, SubjectAlertTrigger}, // first-ever check is down (mon-13)
		{none, deg, SubjectAlertTrigger},
		{none, up, ""},
		{up, down, SubjectAlertTrigger},
		{up, deg, SubjectAlertTrigger},
		{deg, down, SubjectAlertTrigger}, // degraded escalates to down
		{down, down, ""},
		{deg, deg, ""},
		{down, deg, ""},
		{up, up, ""},
		{down, up, SubjectAlertRecover},
		{deg, up, SubjectAlertRecover},
	}
	for _, c := range cases {
		if got := AlertTransition(c.prev, c.cur); got != c.want {
			t.Errorf("AlertTransition(%q, %q) = %q, want %q", c.prev, c.cur, got, c.want)
		}
	}
}
