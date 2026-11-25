package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Test binaries open a fresh ":memory:" store for almost every test. Running
// all migrations on each one is cheap normally (~25ms) but costs ~1s under
// -race, because the pure-Go SQLite engine is fully instrumented, and the
// handler tests alone do it ~110 times. So in test binaries the first empty
// in-memory store is migrated for real and serialized, and later empty
// in-memory stores are restored from that snapshot instead.
//
// Only reached when testing.Testing() is true (see Migrate). A store that
// already has tables (a test exercising migrations themselves) always takes
// the real migration path.
var testSnapshot struct {
	mu  sync.Mutex
	buf []byte
}

type serializer interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

func (s *SQLiteStore) migrateFromTestSnapshot(ctx context.Context) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&tables); err != nil {
		return fmt.Errorf("sqlite test snapshot: count tables: %w", err)
	}
	if tables > 0 {
		return s.migrate(ctx)
	}

	testSnapshot.mu.Lock()
	defer testSnapshot.mu.Unlock()

	if testSnapshot.buf != nil {
		return s.rawConn(ctx, func(c serializer) error { return c.Deserialize(testSnapshot.buf) })
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	// A failed snapshot only costs speed: later stores migrate for real.
	_ = s.rawConn(ctx, func(c serializer) error {
		buf, err := c.Serialize()
		if err == nil && len(buf) > 0 {
			testSnapshot.buf = buf
		}
		return err
	})
	return nil
}

// rawConn runs fn on the store's single underlying driver connection.
func (s *SQLiteStore) rawConn(ctx context.Context, fn func(serializer) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite test snapshot: conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(dc any) error {
		c, ok := dc.(serializer)
		if !ok {
			return errors.New("sqlite test snapshot: driver connection cannot serialize")
		}
		return fn(c)
	})
}
