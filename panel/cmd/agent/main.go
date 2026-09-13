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

var (
	agentHeartbeatInterval     = 10 * time.Second
	agentMetricsInterval       = 5 * time.Second
	agentConfigPollInterval    = 30 * time.Second
	agentClientTrafficInterval = 15 * time.Second
	publicIPv4RefreshInterval  = 45 * time.Minute
	dialAgentWebSocket         = websocket.Dial
	writeAgentHeartbeat        = sendHeartbeat
	writeAgentMetrics          = sendMetrics
	newAgentMetrics            = newMetricsCollector
	collectAgentMetrics        = func(collector *metricsCollector) (metricsMessage, bool) {
		return collector.collect()
	}
	waitAgentReconnect = waitForReconnect
)

const (
	initialReconnectDelay = time.Second
	maximumReconnectDelay = 30 * time.Second
)

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

type publicIPv4State struct {
	detect    func(context.Context) string
	value     string
	checkedAt time.Time
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
	publicIPv4Client := newPublicIPv4HTTPClient()
	return connectAgentWithPublicIPv4(ctx, defaultConfigPath, func(ctx context.Context) string {
		return detectPublicIPv4(ctx, publicIPv4Client, publicIPv4Endpoint)
	})
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
		var responseError struct {
			Error string `json:"error"`
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if readErr == nil && json.Unmarshal(body, &responseError) == nil {
			message := strings.TrimSpace(responseError.Error)
			if message != "" && !strings.Contains(message, enrollmentToken) {
				return config{}, fmt.Errorf("Panel rejected registration: %s (%s)", message, response.Status)
			}
		}
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
	return connectAgentWithPublicIPv4(ctx, configPath, func(context.Context) string { return "" })
}

func connectAgentWithPublicIPv4(ctx context.Context, configPath string, detect func(context.Context) string) error {
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
	configSync := newConfigSynchronizer(value, &http.Client{Timeout: 10 * time.Second})
	publicIPv4 := &publicIPv4State{detect: detect}
	detectionContext, cancelDetection := context.WithTimeout(ctx, publicIPv4RequestTimeout)
	publicIPv4.current(detectionContext)
	cancelDetection()

	reconnectDelay := initialReconnectDelay
	for {
		connected, authenticationRejected := connectAgentOnce(ctx, value, configSync, publicIPv4)
		if ctx.Err() != nil {
			return nil
		}
		if !connected {
			attemptConfigSync(ctx, configSync)
		}
		if authenticationRejected {
			log.Print("Agent authentication rejected by Panel; will retry")
			reconnectDelay = maximumReconnectDelay
		} else if connected {
			log.Print("Panel connection lost; will reconnect")
			reconnectDelay = initialReconnectDelay
		} else {
			log.Print("Panel connection unavailable; will retry")
		}
		log.Printf("reconnecting in %s", reconnectDelay)
		if !waitAgentReconnect(ctx, reconnectDelay) {
			return nil
		}
		if !connected && !authenticationRejected {
			reconnectDelay = nextReconnectDelay(reconnectDelay)
		}
	}
}

func connectAgentOnce(ctx context.Context, value config, configSync *configSynchronizer, publicIPv4 *publicIPv4State) (bool, bool) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+value.AgentToken)
	connection, response, err := dialAgentWebSocket(
		ctx,
		value.PanelURL+"/api/agent/ws",
		&websocket.DialOptions{HTTPHeader: header},
	)
	if err != nil {
		if response != nil {
			response.Body.Close()
			return false, response.StatusCode == http.StatusUnauthorized
		}
		return false, false
	}
	defer connection.CloseNow()
	connection.SetReadLimit(8 << 10)

	log.Printf("vps-panel-agent %s connected for server %d", agentVersion, value.ServerID)
	message := collectSystemInfo()
	detectionContext, cancelDetection := context.WithTimeout(ctx, publicIPv4RequestTimeout)
	message.PublicIPv4 = publicIPv4.current(detectionContext)
	cancelDetection()
	systemInfoContext, cancelSystemInfo := context.WithTimeout(ctx, 5*time.Second)
	err = sendSystemInfo(systemInfoContext, connection, message)
	cancelSystemInfo()
	if err != nil {
		return true, false
	}
	metricsCollector := newAgentMetrics()
	trafficReporter := newClientTrafficReporter(value, &http.Client{Timeout: 10 * time.Second})
	connectionContext, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	configChanged := make(chan struct{}, 1)
	disconnected := make(chan error, 1)
	go func() {
		disconnected <- readPanelMessages(connectionContext, connection, configChanged)
	}()
	attemptConfigSync(ctx, configSync)
	heartbeatTicker := time.NewTicker(agentHeartbeatInterval)
	defer heartbeatTicker.Stop()
	metricsTicker := time.NewTicker(agentMetricsInterval)
	defer metricsTicker.Stop()
	configTicker := time.NewTicker(agentConfigPollInterval)
	defer configTicker.Stop()
	clientTrafficTicker := time.NewTicker(agentClientTrafficInterval)
	defer clientTrafficTicker.Stop()
	publicIPv4Ticker := time.NewTicker(publicIPv4RefreshInterval)
	defer publicIPv4Ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = connection.Close(websocket.StatusNormalClosure, "Agent stopped")
			return true, false
		case <-disconnected:
			return true, false
		case <-configChanged:
			attemptConfigSync(ctx, configSync)
		case <-configTicker.C:
			attemptConfigSync(ctx, configSync)
		case <-clientTrafficTicker.C:
			trafficContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := trafficReporter.report(trafficContext)
			cancel()
			if err != nil {
				log.Printf("report client traffic: %v", err)
			}
		case <-publicIPv4Ticker.C:
			message := collectSystemInfo()
			detectionContext, cancelDetection := context.WithTimeout(ctx, publicIPv4RequestTimeout)
			message.PublicIPv4 = publicIPv4.current(detectionContext)
			cancelDetection()
			systemInfoContext, cancelSystemInfo := context.WithTimeout(ctx, 5*time.Second)
			err := sendSystemInfo(systemInfoContext, connection, message)
			cancelSystemInfo()
			if err != nil {
				return true, false
			}
		case <-heartbeatTicker.C:
			heartbeatContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := writeAgentHeartbeat(heartbeatContext, connection)
			cancel()
			if err != nil {
				return true, false
			}
		case <-metricsTicker.C:
			metrics, ok := collectAgentMetrics(metricsCollector)
			if !ok {
				continue
			}
			metricsContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := writeAgentMetrics(metricsContext, connection, metrics)
			cancel()
			if err != nil {
				return true, false
			}
		}
	}
}

func (state *publicIPv4State) current(ctx context.Context) string {
	now := time.Now()
	if !state.checkedAt.IsZero() && now.Sub(state.checkedAt) < publicIPv4RefreshInterval {
		return state.value
	}
	state.value = state.detect(ctx)
	state.checkedAt = now
	return state.value
}

func readPanelMessages(ctx context.Context, connection *websocket.Conn, configChanged chan<- struct{}) error {
	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		var notification struct {
			Type    string `json:"type"`
			Version int64  `json:"version"`
		}
		if messageType != websocket.MessageText || json.Unmarshal(message, &notification) != nil ||
			notification.Type != "config_changed" || notification.Version <= 0 {
			return errors.New("Panel sent an invalid WebSocket message")
		}
		select {
		case configChanged <- struct{}{}:
		default:
		}
	}
}

func sendHeartbeat(ctx context.Context, connection *websocket.Conn) error {
	return connection.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`))
}

func nextReconnectDelay(current time.Duration) time.Duration {
	if current >= maximumReconnectDelay {
		return maximumReconnectDelay
	}
	next := current * 2
	if next > maximumReconnectDelay {
		return maximumReconnectDelay
	}
	return next
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
