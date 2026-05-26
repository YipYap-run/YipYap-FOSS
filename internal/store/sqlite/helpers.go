package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

const timeFormat = time.RFC3339

func mustParseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		// Fallback for datetime format stored by SQLite datetime('now')
		t, err = time.Parse("2006-01-02T15:04:05Z07:00", s)
		if err != nil {
			t, _ = time.Parse("2006-01-02 15:04:05", s)
		}
	}
	return t
}

func parseOptionalTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t := mustParseTime(*s)
	return &t
}

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

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func expectOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}
