package sqlx

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// MigrationPrecheck inspects the data before a migration runs. It returns an
// error that explains what an operator must fix, so the upgrade stops with
// a clear message instead of a raw constraint failure halfway through.
type MigrationPrecheck func(ctx context.Context, q Queryable, m Migration) error

// migrationPrechecks is keyed by migration file name without the numeric
// prefix, so it survives renumbering at merge time.
var migrationPrechecks = map[string]MigrationPrecheck{
	"users_email_ci.sql": precheckUniqueEmails,
}

func migrationPrecheck(filename string) MigrationPrecheck {
	base := path.Base(filename)
	if i := strings.IndexByte(base, '_'); i >= 0 {
		base = base[i+1:]
	}
	return migrationPrechecks[base]
}

// precheckUniqueEmails refuses the case-insensitive email index while two
// users share an address that differs only by case or surrounding space. It
// reports user ids, never the addresses, so the log carries no personal
// data. Operators look the ids up and rename or remove the extra accounts.
func precheckUniqueEmails(ctx context.Context, q Queryable, m Migration) error {
	rows, err := q.QueryContext(ctx, `
		SELECT id, lower(trim(email)) FROM users
		 WHERE lower(trim(email)) IN (
		       SELECT lower(trim(email)) FROM users
		        GROUP BY lower(trim(email)) HAVING COUNT(*) > 1)
		 ORDER BY lower(trim(email)), id`)
	if err != nil {
		return fmt.Errorf("precheck migration %d (%s): %w", m.Version, m.Filename, err)
	}
	defer func() { _ = rows.Close() }()

	var (
		groups  [][]string
		lastKey string
	)
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return fmt.Errorf("precheck migration %d (%s): %w", m.Version, m.Filename, err)
		}
		if len(groups) == 0 || key != lastKey {
			groups = append(groups, nil)
			lastKey = key
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("precheck migration %d (%s): %w", m.Version, m.Filename, err)
	}
	if len(groups) == 0 {
		return nil
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = "[" + strings.Join(g, ", ") + "]"
	}
	return fmt.Errorf("migration %d (%s) stopped: %d email addresses are used by more than one user when compared without case. "+
		"Each group below is one address; keep one user per group and rename or delete the others, then restart. User ids: %s",
		m.Version, m.Filename, len(groups), strings.Join(parts, " "))
}
