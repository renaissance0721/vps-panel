package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	xrayClientStatsPrefix      = "user>>>vp-client-"
	xrayClientTrafficSeparator = ">>>traffic>>>"
	xrayStatsCommandOutputMax  = 1 << 20
)

type agentClientTrafficItem struct {
	ClientID      int64 `json:"client_id"`
	UplinkBytes   int64 `json:"uplink_bytes"`
	DownlinkBytes int64 `json:"downlink_bytes"`
}

type agentClientTrafficPayload struct {
	Clients []agentClientTrafficItem `json:"clients"`
}

type xrayStatsCollector struct {
	binaryPath string
	configPath string
	apiAddress string
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

type clientTrafficReporter struct {
	config    config
	client    *http.Client
	collector *xrayStatsCollector
}

type xrayStatsResponse struct {
	Stats []xrayStat `json:"stat"`
}

type xrayStat struct {
	Name  string        `json:"name"`
	Value xrayByteCount `json:"value"`
}

type xrayByteCount int64

func (value *xrayByteCount) UnmarshalJSON(data []byte) error {
	normalized := strings.Trim(string(data), `"`)
	parsed, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil || parsed < 0 {
		return errors.New("invalid Xray byte counter")
	}
	*value = xrayByteCount(parsed)
	return nil
}

func newClientTrafficReporter(value config, client *http.Client) *clientTrafficReporter {
	return &clientTrafficReporter{
		config: value,
		client: client,
		collector: &xrayStatsCollector{
			binaryPath: managedXrayBinaryPath,
			configPath: managedXrayConfigPath,
			apiAddress: managedXrayStatsAPIAddress,
			runCommand: runXrayStatsCommand,
		},
	}
}

func (reporter *clientTrafficReporter) report(ctx context.Context) error {
	clients, err := reporter.collector.collect(ctx)
	if err != nil {
		return err
	}
	if len(clients) == 0 {
		return nil
	}
	body, err := json.Marshal(agentClientTrafficPayload{Clients: clients})
	if err != nil {
		return fmt.Errorf("encode client traffic report: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, reporter.config.PanelURL+"/api/agent/traffic", bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create client traffic request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+reporter.config.AgentToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := reporter.client.Do(request)
	if err != nil {
		return fmt.Errorf("report client traffic: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Panel rejected client traffic report: %s", response.Status)
	}
	return nil
}

func (collector *xrayStatsCollector) collect(ctx context.Context) ([]agentClientTrafficItem, error) {
	configured, err := regularFileExists(collector.configPath)
	if err != nil {
		return nil, fmt.Errorf("inspect managed Xray config: %w", err)
	}
	if !configured {
		return nil, nil
	}
	output, err := collector.runCommand(
		ctx, collector.binaryPath, "api", "statsquery", "--server="+collector.apiAddress,
		"-pattern", xrayClientStatsPrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("query Xray client traffic: %w", err)
	}
	return parseXrayClientStats(output)
}

func parseXrayClientStats(output []byte) ([]agentClientTrafficItem, error) {
	var response xrayStatsResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("decode Xray client traffic: %w", err)
	}
	type counters struct {
		uplink, downlink       int64
		hasUplink, hasDownlink bool
	}
	byClient := make(map[int64]counters)
	for _, stat := range response.Stats {
		if !strings.HasPrefix(stat.Name, xrayClientStatsPrefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(stat.Name, xrayClientStatsPrefix), xrayClientTrafficSeparator)
		if len(parts) != 2 {
			return nil, errors.New("Xray returned an invalid client traffic counter")
		}
		clientID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || clientID <= 0 {
			return nil, errors.New("Xray returned an invalid client traffic identifier")
		}
		value := byClient[clientID]
		switch parts[1] {
		case "uplink":
			if value.hasUplink {
				return nil, errors.New("Xray returned a duplicate client uplink counter")
			}
			value.uplink, value.hasUplink = int64(stat.Value), true
		case "downlink":
			if value.hasDownlink {
				return nil, errors.New("Xray returned a duplicate client downlink counter")
			}
			value.downlink, value.hasDownlink = int64(stat.Value), true
		default:
			return nil, errors.New("Xray returned an invalid client traffic direction")
		}
		byClient[clientID] = value
	}
	clientIDs := make([]int64, 0, len(byClient))
	for clientID, value := range byClient {
		if value.hasUplink && value.hasDownlink {
			clientIDs = append(clientIDs, clientID)
		}
	}
	sort.Slice(clientIDs, func(i, j int) bool { return clientIDs[i] < clientIDs[j] })
	clients := make([]agentClientTrafficItem, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		value := byClient[clientID]
		clients = append(clients, agentClientTrafficItem{
			ClientID: clientID, UplinkBytes: value.uplink, DownlinkBytes: value.downlink,
		})
	}
	return clients, nil
}

func runXrayStatsCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	stdout := &cappedBuffer{limit: xrayStatsCommandOutputMax}
	stderr := &cappedBuffer{limit: managedXrayCommandOutputMax}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, truncateDiagnostic(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
