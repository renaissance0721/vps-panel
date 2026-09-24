package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/api"
	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

const defaultHealthcheckURL = "http://127.0.0.1:8080/api/health"

var panelVersion = "dev"
var errRestoreRestart = errors.New("restore staged; restarting panel")

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	listenAddr := envOrDefault("PANEL_LISTEN_ADDR", "127.0.0.1:8080")
	dataDir := envOrDefault("PANEL_DATA_DIR", "data")
	webDir := envOrDefault("PANEL_WEB_DIR", "../web/dist")

	db, err := openDatabase(dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	resetContext, cancelReset := context.WithTimeout(context.Background(), 5*time.Second)
	if err := agentcontrol.NewService(db, time.Now).ResetOnline(resetContext); err != nil {
		cancelReset()
		return fmt.Errorf("prepare server states: %w", err)
	}
	cancelReset()

	restoreRequested := make(chan struct{}, 1)
	server := &http.Server{
		Addr: listenAddr,
		Handler: api.NewHandlerWithBackup(db, webDir, panelVersion, api.BackupConfig{
			DataDir: dataDir, Domain: os.Getenv("PANEL_DOMAIN"),
			EnvironmentFile: "/etc/vps-panel/panel/environment", CaddyFile: "/etc/caddy/vps-panel.caddy",
			RestoreRequested: restoreRequested,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	var renewalWG sync.WaitGroup
	renewals := serverstore.NewService(db)
	if err := renewals.ApplyAutomaticRenewals(shutdownContext); err != nil {
		log.Printf("automatic server renewal failed: %v", err)
	}
	renewalWG.Add(1)
	go func() {
		defer renewalWG.Done()
		runAutomaticRenewalLoop(shutdownContext, renewals)
	}()
	defer func() {
		stop()
		renewalWG.Wait()
	}()

	serverError := make(chan error, 1)
	go func() {
		log.Printf("panel listening on %s", listenAddr)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve panel: %w", err)
		}
	case <-shutdownContext.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("shut down panel: %w", err)
		}
	case <-restoreRequested:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("shut down panel for restore: %w", err)
		}
		return errRestoreRestart
	}

	return nil
}

func runAutomaticRenewalLoop(ctx context.Context, renewals *serverstore.Service) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := renewals.ApplyAutomaticRenewals(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("automatic server renewal failed: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func openDatabase(dataDir string) (*sql.DB, error) {
	attempt, err := backup.ApplyPendingRestore(dataDir)
	if err != nil {
		if errors.Is(err, backup.ErrRecoveryFailed) {
			return nil, err
		}
		log.Printf("pending restore rejected; opening existing database: %v", err)
	}
	db, openErr := database.Open(dataDir)
	if attempt == nil {
		return db, openErr
	}
	if openErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		openErr = backup.ValidateDatabase(ctx, filepath.Join(dataDir, "panel.db"))
		cancel()
	}
	if openErr == nil {
		openErr = attempt.Commit()
		if openErr == nil {
			return db, nil
		}
	}
	if db != nil {
		_ = db.Close()
	}
	if err := attempt.Rollback(); err != nil {
		return nil, fmt.Errorf("restored database failed: %v; rollback failed: %w", openErr, err)
	}
	log.Printf("restored database rejected; original database recovered: %v", openErr)
	return database.Open(dataDir)
}

func healthcheck() error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(envOrDefault("PANEL_HEALTHCHECK_URL", defaultHealthcheckURL))
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", response.Status)
	}

	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
