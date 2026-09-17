package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/api"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

const defaultHealthcheckURL = "http://127.0.0.1:8080/api/health"

var panelVersion = "dev"

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

	db, err := database.Open(dataDir)
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

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           api.NewHandlerWithVersion(db, webDir, panelVersion),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
	}

	return nil
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
