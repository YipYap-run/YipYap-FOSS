package main

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func openStore(cfg *config.Config) (store.Store, error) {
	return sqlite.New(cfg.DBDsn)
}
