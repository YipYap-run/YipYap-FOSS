package sqlite

import (
	"time"
)

const timeFormat = time.RFC3339

func formatOptionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(timeFormat)
	return &s
}

func timeNowUTC() string {
	return time.Now().UTC().Format(timeFormat)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
