package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlx"
)

// emailCIMigrationFS is a two-step schema: a users table as 001 created it,
// then the real case-insensitive email migration.
func emailCIMigrationFS(t *testing.T) fstest.MapFS {
	t.Helper()
	data, err := fs.ReadFile(migrationsFS, "migrations/047_users_email_ci.sql")
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"m/001_users.sql":          {Data: []byte(`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE)`)},
		"m/002_users_email_ci.sql": {Data: data},
	}
}

func TestEmailCIMigrationReportsCaseDuplicates(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	mfs := emailCIMigrationFS(t)

	// Apply only 001, then add users that collide without case.
	only001 := fstest.MapFS{"m/001_users.sql": mfs["m/001_users.sql"]}
	if err := sqlx.Migrate(ctx, s.db, only001, "m", dialect); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{{"u1", "Dup@Example.com"}, {"u2", "dup@example.com "}, {"u3", "solo@example.com"}} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO users (id, email) VALUES (?, ?)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}

	err = sqlx.Migrate(ctx, s.db, mfs, "m", dialect)
	if err == nil {
		t.Fatal("migration ran over case-duplicate emails")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[u1, u2]") || strings.Contains(msg, "u3") {
		t.Fatalf("precheck message does not list the duplicate group: %s", msg)
	}
	if strings.Contains(strings.ToLower(msg), "example.com") {
		t.Fatalf("precheck message leaks email addresses: %s", msg)
	}
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("migration recorded despite the precheck: v=%d err=%v", v, err)
	}

	// After the operator resolves the duplicate the migration applies,
	// lower-cases stored addresses and enforces the index.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u2'`); err != nil {
		t.Fatal(err)
	}
	if err := sqlx.Migrate(ctx, s.db, mfs, "m", dialect); err != nil {
		t.Fatalf("migration after cleanup: %v", err)
	}
	var email string
	if err := s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = 'u1'`).Scan(&email); err != nil || email != "dup@example.com" {
		t.Fatalf("stored email not normalized: %q %v", email, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (id, email) VALUES ('u4', 'SOLO@example.com')`); err == nil {
		t.Fatal("case-insensitive unique index not enforced")
	}
}
