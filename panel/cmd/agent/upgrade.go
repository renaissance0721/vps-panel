package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/version"
)

const (
	agentServiceName = "vps-panel-agent"
)

var (
	agentManagedDir   = "/opt/vps-panel/agent"
	agentManagedPath  = agentManagedDir + "/vps-panel-agent"
	releasesBaseURL   = "https://github.com/renaissance0721/vps-panel/releases/download"
	runUpgradeCommand = func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	}
	runUpgradeLauncher = func(name string, arguments ...string) ([]byte, error) {
		return exec.Command(name, arguments...).CombinedOutput()
	}
	runAgentServiceCommand = func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	}
	detectUpgradeHostEnvironment = detectHostEnvironment
	startOpenRCUpgradeProcess    = startDetachedUpgradeProcess
	launchAgentUpgrade           = launchUpgradeHelper
)

type agentUpgradeResult struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func prepareAgentUpgrade(ctx context.Context, client *http.Client, value config, targetVersion string) error {
	assetName, err := agentReleaseAsset(runtime.GOARCH)
	if err != nil {
		return err
	}
	if !isFormalAgentVersion(targetVersion) {
		return errors.New("Panel requested an invalid Agent release version")
	}
	comparison, ok := version.Compare(agentVersion, targetVersion)
	if !ok {
		return errors.New("cannot compare Agent and Panel release versions")
	}
	if comparison == 0 {
		return errors.New("Agent already runs the requested release")
	}
	if comparison > 0 {
		return fmt.Errorf("Agent %s is newer than Panel %s; automatic downgrade is not supported", agentVersion, targetVersion)
	}
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		return fmt.Errorf("create managed Agent directory: %w", err)
	}
	temporary, err := os.CreateTemp(agentManagedDir, ".agent-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temporary Agent binary: %w", err)
	}
	stagedPath := temporary.Name()
	keepStaged := false
	defer func() {
		temporary.Close()
		if !keepStaged {
			os.Remove(stagedPath)
		}
	}()

	baseURL := releasesBaseURL + "/" + targetVersion
	if err := downloadUpgradeFile(ctx, client, baseURL+"/"+assetName, temporary); err != nil {
		return fmt.Errorf("download Agent upgrade: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync Agent upgrade: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent upgrade: %w", err)
	}
	checksumData, err := downloadUpgradeBytes(ctx, client, baseURL+"/SHA256SUMS", 1<<20)
	if err != nil {
		return fmt.Errorf("download Agent checksums: %w", err)
	}
	if err := verifyAgentChecksum(stagedPath, assetName, checksumData); err != nil {
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return fmt.Errorf("make Agent upgrade executable: %w", err)
	}
	if err := verifyAgentBinaryVersion(ctx, stagedPath, targetVersion); err != nil {
		return err
	}
	if err := launchAgentUpgrade(stagedPath, targetVersion); err != nil {
		return err
	}
	keepStaged = true
	return nil
}

func agentReleaseAsset(architecture string) (string, error) {
	switch architecture {
	case "amd64", "arm64":
		return "vps-panel-agent-linux-" + architecture, nil
	default:
		return "", fmt.Errorf("unsupported Agent architecture: %s", architecture)
	}
}

func isFormalAgentVersion(value string) bool {
	if !strings.HasPrefix(value, "v") {
		return false
	}
	parts := strings.Split(value[1:], ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func downloadUpgradeFile(ctx context.Context, client *http.Client, url string, destination io.Writer) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", response.Status)
	}
	if _, err := io.Copy(destination, response.Body); err != nil {
		return err
	}
	return nil
}

func downloadUpgradeBytes(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download is too large")
	}
	return data, nil
}

func verifyAgentChecksum(path, assetName string, checksums []byte) error {
	expected := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if len(expected) != sha256.Size*2 {
		return errors.New("Agent checksum is missing from SHA256SUMS")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Agent upgrade for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("calculate Agent checksum: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("Agent SHA256 checksum does not match")
	}
	return nil
}

func verifyAgentBinaryVersion(ctx context.Context, path, targetVersion string) error {
	checkContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := runUpgradeCommand(checkContext, path, "version")
	if err != nil {
		return fmt.Errorf("run upgraded Agent version check: %w", err)
	}
	if strings.TrimSpace(string(output)) != "vps-panel-agent "+targetVersion {
		return errors.New("downloaded Agent version does not match upgrade target")
	}
	return nil
}

func launchUpgradeHelper(stagedPath, targetVersion string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current Agent binary: %w", err)
	}
	environment, err := detectUpgradeHostEnvironment()
	if err != nil {
		return err
	}
	arguments := []string{"_apply-upgrade", "--staged", stagedPath, "--target", targetVersion}
	if environment.InitSystem == initSystemOpenRC {
		if err := startOpenRCUpgradeProcess(executable, arguments...); err != nil {
			return fmt.Errorf("start detached OpenRC Agent upgrade helper: %w", err)
		}
		return nil
	}
	if environment.InitSystem != initSystemSystemd {
		return errUnsupportedInitSystem
	}
	unit := fmt.Sprintf("vps-panel-agent-upgrade-%d", time.Now().UnixNano())
	launcherArguments := append([]string{"--quiet", "--collect", "--unit=" + unit, executable}, arguments...)
	output, err := runUpgradeLauncher("systemd-run", launcherArguments...)
	if err != nil {
		return fmt.Errorf("start Agent upgrade helper: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func applyStagedAgentUpgrade(stagedPath, targetVersion string) error {
	if filepath.Dir(stagedPath) != agentManagedDir || !isFormalAgentVersion(targetVersion) {
		return errors.New("invalid staged Agent upgrade")
	}
	rollback, err := copyAgentBinary(agentManagedPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if rollback != "" {
		defer os.Remove(rollback)
	}
	if err := os.Rename(stagedPath, agentManagedPath); err != nil {
		return fmt.Errorf("replace Agent binary: %w", err)
	}
	if err := os.Chmod(agentManagedPath, 0o755); err != nil {
		return rollbackAgentBinary(rollback, fmt.Errorf("secure Agent binary: %w", err))
	}
	if err := restartAgentService(); err != nil {
		return rollbackAgentBinary(rollback, err)
	}
	return nil
}

func copyAgentBinary(source string) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.CreateTemp(agentManagedDir, ".agent-rollback-*")
	if err != nil {
		return "", fmt.Errorf("create Agent rollback: %w", err)
	}
	path := output.Name()
	defer func() {
		if err != nil {
			os.Remove(path)
		}
	}()
	if _, err = io.Copy(output, input); err == nil {
		err = output.Chmod(0o755)
	}
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("prepare Agent rollback: %w", err)
	}
	return path, nil
}

func rollbackAgentBinary(rollback string, upgradeErr error) error {
	if rollback == "" {
		return upgradeErr
	}
	if err := os.Rename(rollback, agentManagedPath); err != nil {
		return fmt.Errorf("%v; restore previous Agent: %w", upgradeErr, err)
	}
	if err := restartAgentService(); err != nil {
		return fmt.Errorf("%v; restart restored Agent: %w", upgradeErr, err)
	}
	return upgradeErr
}

func restartAgentService() error {
	environment, err := detectUpgradeHostEnvironment()
	if err != nil {
		return err
	}
	manager := newServiceManager(environment.InitSystem, agentServiceDefinition(), "", runAgentServiceCommand)
	if err := manager.Restart(context.Background()); err != nil {
		return fmt.Errorf("restart Agent service: %w", err)
	}
	active, err := manager.IsActive(context.Background())
	if err != nil {
		return fmt.Errorf("verify Agent service: %w", err)
	}
	if !active {
		return errors.New("verify Agent service: service is not active")
	}
	return nil
}

func agentServiceDefinition() serviceDefinition {
	return serviceDefinition{
		Name:        agentServiceName,
		Description: "VPS Panel Agent",
		Command:     agentManagedPath,
	}
}

func reportAgentUpgradeFailure(ctx context.Context, client *http.Client, value config, version string, upgradeErr error) {
	message := strings.TrimSpace(upgradeErr.Error())
	if message == "" || strings.Contains(message, value.AgentToken) {
		message = "Agent upgrade failed"
	}
	body, err := json.Marshal(agentUpgradeResult{Version: version, Status: "failed", Message: message})
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, value.PanelURL+"/api/agent/upgrade/result", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+value.AgentToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
	}
}
