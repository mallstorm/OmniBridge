package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"omnibridge/internal/accounts"
	"omnibridge/internal/config"
	"omnibridge/internal/health"
	"omnibridge/internal/providers"
	"omnibridge/internal/registry"
	"omnibridge/internal/secretstore"
	"omnibridge/internal/security"
	"omnibridge/internal/server"
	"omnibridge/internal/stats"
	"omnibridge/internal/storage"
)

type coreRuntime struct {
	server   *server.Server
	accounts *accounts.Manager
	stats    *stats.Store
	db       *storage.DB
	cancel   context.CancelFunc
}

func main() {
	root, err := applicationRoot()
	if err != nil {
		fatal(nil, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o700); err != nil {
		fatal(nil, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o700); err != nil {
		fatal(nil, err)
	}
	lf, err := os.OpenFile(filepath.Join(root, "logs", "bridge.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fatal(nil, err)
	}
	defer lf.Close()
	logger := log.New(security.NewRedactingWriter(lf), "", log.LstdFlags|log.Lmicroseconds)

	if integratedDesktopSupported() {
		_ = os.Remove(filepath.Join(root, "data", "restart.request"))
		if err := runIntegrated(root, logger); err != nil {
			fatal(logger, err)
		}
		return
	}
	if err := runHeadless(root, logger); err != nil {
		fatal(logger, err)
	}
}

func applicationRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	root := filepath.Dir(exe)
	if v := os.Getenv("OMNIBRIDGE_HOME"); v != "" {
		root = v
	} else if v := os.Getenv("UNIVERSAL_BRIDGE_HOME"); v != "" {
		root = v
	}
	return root, nil
}

func newCore(root string, logger *log.Logger) (*coreRuntime, error) {
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
	return &coreRuntime{server: srv, accounts: am, stats: st, db: db, cancel: cancel}, nil
}

func (c *coreRuntime) close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.accounts != nil {
		c.accounts.Close()
	}
	if c.stats != nil {
		_ = c.stats.Flush()
	}
	if c.db != nil {
		_ = c.db.Close()
	}
}

func runHeadless(root string, logger *log.Logger) error {
	core, err := newCore(root, logger)
	if err != nil {
		return err
	}
	defer core.close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = core.server.Stop(ctx)
	}()
	err = core.server.StartOrchestrated()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runIntegrated(root string, logger *log.Logger) error {
	for {
		restart, err := runIntegratedOnce(root, logger)
		if err != nil {
			return err
		}
		if !restart {
			return nil
		}
		logger.Printf("integrated application restart requested")
	}
}

func runIntegratedOnce(root string, logger *log.Logger) (bool, error) {
	core, err := newCore(root, logger)
	if err != nil {
		return false, err
	}
	defer core.close()
	serverErr := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("[recovery] integrated Core panic: %v\n%s", recovered, debug.Stack())
				serverErr <- fmt.Errorf("integrated Core recovered from panic: %v", recovered)
			}
		}()
		serverErr <- core.server.StartOrchestrated()
	}()
	if err := waitForCore(core.server.Config.Get().Listen, serverErr); err != nil {
		return false, err
	}

	page := "dashboard"
	if len(os.Args) > 1 && os.Args[1] != "" {
		page = os.Args[1]
	}
	exitCode, uiErr := launchIntegratedDesktop(root, page)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = core.server.Stop(ctx)
	cancel()
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return false, err
		}
	case <-time.After(3500 * time.Millisecond):
		return false, errors.New("integrated Core did not stop")
	}
	restart := consumeRestartRequest(root)
	if uiErr != nil && exitCode != 0 {
		return restart, fmt.Errorf("integrated desktop exited with code %d: %w", exitCode, uiErr)
	}
	return restart, nil
}

func waitForCore(listen string, serverErr <-chan error) error {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	url := "http://" + listen + "/health"
	for attempt := 0; attempt < 60; attempt++ {
		select {
		case err := <-serverErr:
			if err == nil {
				return errors.New("Core stopped before Desktop started")
			}
			return err
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("Core did not become ready for the integrated Desktop")
}

func consumeRestartRequest(root string) bool {
	marker := filepath.Join(root, "data", "restart.request")
	if _, err := os.Stat(marker); err != nil {
		return false
	}
	_ = os.Remove(marker)
	return true
}

func fatal(logger *log.Logger, err error) {
	if logger != nil {
		logger.Printf("fatal: %v", err)
	}
	showStartupError("OmniBridge", err.Error())
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
