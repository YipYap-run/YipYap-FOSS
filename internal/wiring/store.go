package wiring

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// OpenStore connects to the SQLite backend (the only one in FOSS builds).
func OpenStore(cfg *config.Config) (store.Store, error) {
	return sqlite.New(cfg.DBDsn)
}
