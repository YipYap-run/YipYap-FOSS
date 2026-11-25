package sqlx

import (
	"context"
	"database/sql"
	"fmt"
)

// txBeginner is satisfied by *sql.DB. A Queryable that is already a *sql.Tx
// does not implement it, so nested calls reuse the caller's transaction.
type txBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// InTx runs fn inside a transaction on q. When q is already a transaction (the
// store was built by Store.Tx), fn runs on it directly and the caller owns
// commit and rollback. A RebindingQueryable is unwrapped to begin the
// transaction and re-wrapped around it so placeholders keep being rewritten.
func InTx(ctx context.Context, q Queryable, fn func(Queryable) error) error {
	inner := q
	rebind := false
	if r, ok := q.(RebindingQueryable); ok {
		inner = r.Q
		rebind = true
	}
	b, ok := inner.(txBeginner)
	if !ok {
		return fn(q)
	}
	tx, err := b.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	var txq Queryable = tx
	if rebind {
		txq = RebindingQueryable{Q: tx}
	}
	if err := fn(txq); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
