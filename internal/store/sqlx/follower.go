package sqlx

import (
	"context"
	"database/sql"
)

// FollowerReadTx starts a read-only transaction with YugabyteDB follower reads
// enabled. This lets the read be served by the nearest replica instead of the
// leader tablet, reducing cross-region latency.
//
// On standard PostgreSQL the SET LOCAL statements are no-ops (unknown variables
// are silently ignored inside a transaction), so this is safe to call against
// any PostgreSQL-compatible database. Callers should Commit (a no-op rollback
// for a read-only transaction) when done.
func FollowerReadTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	_, _ = tx.ExecContext(ctx, "SET LOCAL yb_read_from_followers = true")
	_, _ = tx.ExecContext(ctx, "SET LOCAL yb_follower_read_staleness_ms = 5000")
	return tx, nil
}
