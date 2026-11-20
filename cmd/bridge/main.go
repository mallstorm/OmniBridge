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

	"omnibridge/internal/app"
	"omnibridge/internal/security"
)

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

	if len(os.Args) > 1 && os.Args[1] == "keys" {
		out, err := app.KeysJSON(root)
		if err != nil {
			fatal(logger, err)
		}
		_, _ = os.Stdout.Write(out)
		return
	}
	if os.Getenv("OMNIBRIDGE_HEADLESS") == "1" {
		if err := runHeadless(root, logger); err != nil {
			fatal(logger, err)
		}
		return
	}
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

func runHeadless(root string, logger *log.Logger) error {
	core, err := app.NewCore(root, logger)
	if err != nil {
		return err
	}
	defer core.Close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = core.Server.Stop(ctx)
	}()
	err = core.Server.Start()
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
	core, err := app.NewCore(root, logger)
	if err != nil {
		return false, err
	}
	defer core.Close()
	serverErr := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("[recovery] integrated Core panic: %v\n%s", recovered, debug.Stack())
				serverErr <- fmt.Errorf("integrated Core recovered from panic: %v", recovered)
			}
		}()
		serverErr <- core.Server.Start()
	}()
	if err := waitForCore(core.Server.Config.Get().Listen, serverErr); err != nil {
		return false, err
	}

	page := "dashboard"
	if len(os.Args) > 1 && os.Args[1] != "" {
		page = os.Args[1]
	}
	exitCode, uiErr := launchIntegratedDesktop(root, page)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = core.Server.Stop(ctx)
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
