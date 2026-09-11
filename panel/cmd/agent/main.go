package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

const defaultConfigPath = "/etc/vps-panel-agent/config.json"

var agentVersion = "dev"

type registrationRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	AgentVersion    string `json:"agent_version"`
	ExistingConfig  bool   `json:"existing_config"`
}

type registrationResponse struct {
	AgentID    int64  `json:"agent_id"`
	ServerID   int64  `json:"server_id"`
	AgentToken string `json:"agent_token"`
}

type config struct {
	PanelURL   string `json:"panel_url"`
	ServerID   int64  `json:"server_id"`
	AgentID    int64  `json:"agent_id"`
	AgentToken string `json:"agent_token"`
}

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		log.Printf("vps-panel-agent: %v", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 1 && (arguments[0] == "version" || arguments[0] == "--version") {
		fmt.Printf("vps-panel-agent %s\n", agentVersion)
		return nil
	}
	if len(arguments) > 0 && arguments[0] == "register" {
		return runRegistration(arguments[1:])
	}
	if len(arguments) != 0 {
		return errors.New("usage: vps-panel-agent [version | register --server URL --token TOKEN]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return connectAgent(ctx, defaultConfigPath)
}

func runRegistration(arguments []string) error {
	flags := flag.NewFlagSet("register", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serverURL := flags.String("server", "", "Panel URL")
	enrollmentToken := flags.String("token", "", "one-time enrollment token")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse registration arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected registration argument")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := registerAgent(
		ctx,
		&http.Client{Timeout: 30 * time.Second},
		*serverURL,
		*enrollmentToken,
		defaultConfigPath,
	)
	if err != nil {
		return err
	}
	fmt.Println("Agent registered successfully.")
	return nil
}

func registerAgent(
	ctx context.Context,
	client *http.Client,
	panelURL string,
	enrollmentToken string,
	configPath string,
) (config, error) {
	panelURL, err := normalizePanelURL(panelURL)
	if err != nil {
		return config{}, err
	}
	if strings.TrimSpace(enrollmentToken) == "" {
		return config{}, errors.New("enrollment token is required")
	}
	existingConfig, err := prepareConfigTarget(configPath)
	if err != nil {
		return config{}, err
	}

	body, err := json.Marshal(registrationRequest{
		EnrollmentToken: enrollmentToken,
		AgentVersion:    agentVersion,
		ExistingConfig:  existingConfig,
	})
	if err != nil {
		return config{}, fmt.Errorf("encode registration request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, panelURL+"/api/agent/register", bytes.NewReader(body),
	)
	if err != nil {
		return config{}, fmt.Errorf("create registration request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return config{}, fmt.Errorf("register with Panel: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return config{}, fmt.Errorf("Panel rejected registration: %s", response.Status)
	}

	var registered registrationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&registered); err != nil {
		return config{}, fmt.Errorf("decode registration response: %w", err)
	}
	if registered.AgentID <= 0 || registered.ServerID <= 0 || registered.AgentToken == "" {
		return config{}, errors.New("Panel returned an invalid registration response")
	}

	value := config{
		PanelURL:   panelURL,
		ServerID:   registered.ServerID,
		AgentID:    registered.AgentID,
		AgentToken: registered.AgentToken,
	}
	if err := saveConfig(configPath, value); err != nil {
		return config{}, err
	}
	return value, nil
}

func normalizePanelURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("server must be a valid HTTP or HTTPS URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("server URL must not contain a query or fragment")
	}
	return value, nil
}

func prepareConfigTarget(path string) (bool, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return false, fmt.Errorf("secure config directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check Agent config: %w", err)
	}
	return false, nil
}

func saveConfig(path string, value config) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Agent config: %w", err)
	}
	data = append(data, '\n')

	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary Agent config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary Agent config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write Agent config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Agent config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install Agent config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure Agent config: %w", err)
	}
	return nil
}

func connectAgent(ctx context.Context, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read Agent config: %w", err)
	}
	var value config
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode Agent config: %w", err)
	}
	if value.PanelURL == "" || value.ServerID <= 0 || value.AgentID <= 0 || value.AgentToken == "" {
		return errors.New("Agent config is incomplete")
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+value.AgentToken)
	connection, response, err := websocket.Dial(
		ctx,
		value.PanelURL+"/api/agent/ws",
		&websocket.DialOptions{HTTPHeader: header},
	)
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect to Panel WebSocket: %s", response.Status)
		}
		return fmt.Errorf("connect to Panel WebSocket: %w", err)
	}
	defer connection.CloseNow()

	log.Printf("vps-panel-agent %s connected for server %d", agentVersion, value.ServerID)
	disconnected := connection.CloseRead(context.Background())
	select {
	case <-ctx.Done():
		_ = connection.Close(websocket.StatusNormalClosure, "Agent stopped")
		return nil
	case <-disconnected.Done():
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("Panel WebSocket connection closed")
	}
}
