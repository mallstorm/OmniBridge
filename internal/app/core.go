// Package app assembles the OmniBridge Core runtime (storage, config, providers,
// accounts, registry, stats, health, HTTP server) so that the headless bridge
// binary and the native Hub shell share one startup path.
package app

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/health"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/server"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
	"path/filepath"
)

// Core is a running OmniBridge Core runtime. Server is the HTTP gateway,
// Cancel stops background refresh.
type Core struct {
	Server   *server.Server
	Accounts *accounts.Manager
	Stats    *stats.Store
	DB       *storage.DB
	Cancel   context.CancelFunc
}

func NewCore(root string, logger *log.Logger) (*Core, error) {
	data := filepath.Join(root, "data")
	db, err := storage.Open(filepath.Join(data, "omnibridge.db"))
	if err != nil {
		return nil, fmt.Errorf("open persistence 2.0 database: %w", err)
	}
	importReport, err := db.ImportLegacy(data)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("import legacy persistence: %w", err)
	}
	if importReport.Imported {
		logger.Printf("[storage] legacy persistence imported: providers=%d accounts=%d models=%d usage_events=%d health_events=%d backup=%s", importReport.Providers, importReport.Accounts, importReport.Models, importReport.UsageEvents, importReport.HealthEvents, importReport.BackupDir)
	}
	cfg, err := config.NewPersistent(filepath.Join(data, "config.json"), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	secrets := secretstore.New(filepath.Join(data, "secrets.json"))
	if err := cfg.EnsureProtectedBridgeKeys(secrets); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect bridge keys: %w", err)
	}
	pm := providers.NewManager(secrets, filepath.Join(data, "russian_trusted_root_ca_pem.crt"))
	am := accounts.NewManager(root, secrets)
	reg := registry.NewPersistent(filepath.Join(data, "model-registry.json"), db, cfg, pm, am)
	st := stats.NewPersistent(filepath.Join(data, "usage-stats.json"), db)
	hs := health.NewPersistent(filepath.Join(data, "connection-health.json"), db)
	srv := server.New(cfg, secrets, pm, am, reg, st, logger, hs)
	srv.SetStorage(db)
	srv.SetRoot(root)
	refreshCtx, cancel := context.WithCancel(context.Background())
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("[recovery] background refresh panic: %v\n%s", recovered, debug.Stack())
			}
		}()
		ctx, stop := context.WithTimeout(refreshCtx, 90*time.Second)
		srv.BackgroundRefresh(ctx)
		stop()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				ctx, stop := context.WithTimeout(refreshCtx, 90*time.Second)
				srv.BackgroundRefresh(ctx)
				stop()
			}
		}
	}()
	return &Core{Server: srv, Accounts: am, Stats: st, DB: db, Cancel: cancel}, nil
}

func (c *Core) Close() {
	if c.Cancel != nil {
		c.Cancel()
	}
	if c.Accounts != nil {
		c.Accounts.Close()
	}
	if c.Stats != nil {
		_ = c.Stats.Flush()
	}
	if c.DB != nil {
		_ = c.DB.Close()
	}
}
