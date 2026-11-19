package sqlx

import (
	"database/sql"
	"fmt"
	"time"
)

// Time is a nullable time that scans from either a native time.Time (Postgres)
// or a string in one of the formats SQLite stores (RFC3339, the
// datetime('now') layout, or "2006-01-02 15:04:05"). Both backends scan into
// this type so the store implementations stay uniform.
type Time struct {
	T     time.Time
	Valid bool
}

func (t *Time) Scan(src any) error {
	if src == nil {
		t.T, t.Valid = time.Time{}, false
		return nil
	}
	var s string
	switch v := src.(type) {
	case time.Time:
		t.T, t.Valid = v, true
		return nil
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		return fmt.Errorf("sqlx.Time: unsupported source type %T", src)
	}
	return t.scanString(s)
}

func (t *Time) scanString(s string) error {
	if s == "" {
		t.T, t.Valid = time.Time{}, false
		return nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.T, t.Valid = parsed, true
			return nil
		}
	}
	// Last resort: database/sql's own parser handles a few more variants.
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	t.T, t.Valid = parsed, true
	return nil
}

// TimePtr returns the *time.Time value or nil when invalid.
func (t Time) TimePtr() *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.T
}

// Bool scans from a native BOOLEAN (Postgres) or an INTEGER 0/1 (SQLite).
type Bool bool

func (b *Bool) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*b = false
	case bool:
		*b = Bool(v)
	case int64:
		*b = Bool(v != 0)
	case int:
		*b = Bool(v != 0)
	case []byte:
		*b = Bool(len(v) > 0 && v[0] != '0')
	case string:
		*b = Bool(v != "" && v != "0" && v != "false")
	default:
		*b = false
	}
	return nil
}

var _ sql.Scanner = (*Time)(nil)
var _ sql.Scanner = (*Bool)(nil)
