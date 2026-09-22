package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
)

const (
	agentDiagnosticTimeout = 8 * time.Second
	diagnosticProbeTimeout = 2 * time.Second
	maxDiagnosticProbes    = 8
	maxDiagnosticFileBytes = 4 << 20
)

type agentDiagnosticRunner struct {
	xray     *xrayManager
	realm    *realmManager
	now      func() time.Time
	lookupIP func(context.Context, string) ([]net.IPAddr, error)
	dialTCP  func(context.Context, string) (net.Conn, error)
}

func newAgentDiagnosticRunner(xray *xrayManager, realm *realmManager) *agentDiagnosticRunner {
	return &agentDiagnosticRunner{
		xray: xray, realm: realm, now: time.Now,
		lookupIP: net.DefaultResolver.LookupIPAddr,
		dialTCP: func(ctx context.Context, endpoint string) (net.Conn, error) {
			return (&net.Dialer{Timeout: diagnosticProbeTimeout}).DialContext(ctx, "tcp", endpoint)
		},
	}
}

func (runner *agentDiagnosticRunner) run(ctx context.Context, state desiredState) []diagnostic.Check {
	checks := make([]diagnostic.Check, 0, 5+len(state.Xray.Proxies)*2+len(state.Realm.Relays)*4)
	checks = append(checks, runner.diagnoseXray(ctx, state.Xray)...)
	checks = append(checks, runner.diagnoseRealm(ctx, state.Realm)...)
	checks = append(checks, runner.diagnoseRelayTargets(ctx, state.Realm.Relays)...)
	checks = append(checks, runner.diagnoseTLS(state.Xray.Proxies)...)
	return checks
}

func (runner *agentDiagnosticRunner) diagnoseXray(ctx context.Context, state desiredXrayState) []diagnostic.Check {
	if !state.Enabled {
		return []diagnostic.Check{
			{Code: "xray.service", Status: diagnostic.StatusSkipped, Detail: "当前 desired state 未启用 Xray"},
			{Code: "xray.config", Status: diagnostic.StatusSkipped, Detail: "当前 desired state 未启用 Xray"},
		}
	}
	checks := make([]diagnostic.Check, 0, 2+len(state.Proxies))
	active, err := runner.xray.isActive(ctx)
	checks = append(checks, serviceDiagnosticCheck("xray.service", "Xray", active, err))

	configCheck := diagnostic.Check{Code: "xray.config", Status: diagnostic.StatusPass, Detail: "当前 Xray 配置语法有效"}
	managed, markerErr := regularFileExists(runner.xray.markerPath)
	binary, binaryErr := regularFileExists(runner.xray.binaryPath)
	config, configErr := regularFileExists(runner.xray.configPath)
	switch {
	case markerErr != nil || binaryErr != nil || configErr != nil:
		configCheck.Status = diagnostic.StatusFail
		configCheck.Detail = "无法读取 Managed Xray 文件状态"
	case !managed || !binary:
		configCheck.Status = diagnostic.StatusFail
		configCheck.Detail = "Managed Xray binary 不存在"
	case !config:
		configCheck.Status = diagnostic.StatusFail
		configCheck.Detail = "当前 Xray 配置不存在"
	case runner.xray.validateConfig(ctx, runner.xray.configPath) != nil:
		configCheck.Status = diagnostic.StatusFail
		configCheck.Detail = "当前 Xray 配置语法校验失败"
	}
	checks = append(checks, configCheck)

	for _, proxy := range state.Proxies {
		id := proxy.ID
		check := diagnostic.Check{
			Code: "xray.listener", Status: diagnostic.StatusPass, ResourceID: &id,
			Endpoint: net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.Port)), Protocol: "tcp",
			Detail: "Xray 本地 TCP listener 正在监听",
		}
		if err := runner.xray.probeListener(ctx, proxy.Port); err != nil {
			check.Status = diagnostic.StatusFail
			check.Detail = safeDiagnosticError("Xray 本地 TCP listener 不可达", err)
		}
		checks = append(checks, check)
	}
	return checks
}

func (runner *agentDiagnosticRunner) diagnoseRealm(ctx context.Context, state desiredRealmState) []diagnostic.Check {
	if !state.Enabled {
		return []diagnostic.Check{{Code: "realm.service", Status: diagnostic.StatusSkipped, Detail: "当前 desired state 未启用 Realm"}}
	}
	checks := make([]diagnostic.Check, 0, 1+len(state.Relays)*2)
	active, err := runner.realm.isActive(ctx)
	checks = append(checks, serviceDiagnosticCheck("realm.service", "Realm", active, err))
	for _, relay := range state.Relays {
		noTCP, useUDP, networkErr := realmNetworkOptions(relay.Network)
		id := relay.ID
		endpoint := net.JoinHostPort(relay.ListenAddress, strconv.Itoa(relay.ListenPort))
		if networkErr != nil {
			checks = append(checks, diagnostic.Check{
				Code: "realm.listener", Status: diagnostic.StatusFail, ResourceID: &id,
				Endpoint: endpoint, Detail: "Relay Network 配置无效",
			})
			continue
		}
		if !noTCP {
			check := diagnostic.Check{
				Code: "realm.listener", Status: diagnostic.StatusPass, ResourceID: &id,
				Endpoint: endpoint, Protocol: "tcp", Detail: "Realm 本地 TCP listener 正在监听",
			}
			if err := runner.realm.probeTCP(ctx, relay.ListenAddress, relay.ListenPort); err != nil {
				check.Status = diagnostic.StatusFail
				check.Detail = safeDiagnosticError("Realm 本地 TCP listener 不可达", err)
			}
			checks = append(checks, check)
		}
		if useUDP {
			check := diagnostic.Check{
				Code: "realm.listener", Status: diagnostic.StatusPass, ResourceID: &id,
				Endpoint: endpoint, Protocol: "udp", Detail: "Realm 本地 UDP listener 存在",
			}
			if err := runner.realm.probeUDP(relay.ListenAddress, relay.ListenPort); err != nil {
				check.Status = diagnostic.StatusFail
				check.Detail = safeDiagnosticError("Realm 本地 UDP listener 不存在", err)
			}
			checks = append(checks, check)
		}
	}
	return checks
}

func (runner *agentDiagnosticRunner) diagnoseRelayTargets(ctx context.Context, relays []desiredRelay) []diagnostic.Check {
	if len(relays) == 0 {
		return nil
	}
	type job struct {
		index int
		relay desiredRelay
	}
	jobs := make(chan job)
	results := make([][]diagnostic.Check, len(relays))
	workers := min(maxDiagnosticProbes, len(relays))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for value := range jobs {
				results[value.index] = runner.diagnoseRelayTarget(ctx, value.relay)
			}
		}()
	}
	for index, relay := range relays {
		jobs <- job{index: index, relay: relay}
	}
	close(jobs)
	group.Wait()
	checks := make([]diagnostic.Check, 0, len(relays)*2)
	for _, result := range results {
		checks = append(checks, result...)
	}
	return checks
}

func (runner *agentDiagnosticRunner) diagnoseRelayTarget(ctx context.Context, relay desiredRelay) []diagnostic.Check {
	id := relay.ID
	host := strings.Trim(relay.TargetHost, "[]")
	endpoint := net.JoinHostPort(host, strconv.Itoa(relay.TargetPort))
	dnsCheck := diagnostic.Check{Code: "relay.dns", ResourceID: &id, Endpoint: host}
	if net.ParseIP(host) != nil {
		dnsCheck.Status = diagnostic.StatusSkipped
		dnsCheck.Detail = "目标已经是 IP 地址，无需 DNS 解析"
	} else {
		dnsContext, cancel := context.WithTimeout(ctx, diagnosticProbeTimeout)
		addresses, err := runner.lookupIP(dnsContext, host)
		cancel()
		if err != nil || len(addresses) == 0 {
			dnsCheck.Status = diagnostic.StatusFail
			dnsCheck.Detail = safeDiagnosticError("DNS 解析失败", err)
		} else {
			dnsCheck.Status = diagnostic.StatusPass
			dnsCheck.Detail = "解析地址：" + diagnosticAddressSummary(addresses)
		}
	}

	tcpCheck := diagnostic.Check{Code: "relay.target_tcp", ResourceID: &id, Endpoint: endpoint, Protocol: "tcp"}
	noTCP, _, networkErr := realmNetworkOptions(relay.Network)
	if networkErr != nil {
		tcpCheck.Status = diagnostic.StatusFail
		tcpCheck.Detail = "Relay Network 配置无效"
	} else if noTCP {
		tcpCheck.Status = diagnostic.StatusSkipped
		tcpCheck.Protocol = "udp"
		tcpCheck.Detail = "远端 UDP 可达性未检测"
	} else {
		probeContext, cancel := context.WithTimeout(ctx, diagnosticProbeTimeout)
		startedAt := time.Now()
		connection, err := runner.dialTCP(probeContext, endpoint)
		latency := time.Since(startedAt).Milliseconds()
		cancel()
		if err != nil {
			tcpCheck.Status = diagnostic.StatusFail
			tcpCheck.Detail = safeDiagnosticError("Relay 目标 TCP 连接失败", err)
		} else {
			_ = connection.Close()
			tcpCheck.Status = diagnostic.StatusPass
			tcpCheck.LatencyMS = &latency
			tcpCheck.Detail = "Agent 所在网络可以建立目标 TCP 连接"
		}
	}
	return []diagnostic.Check{dnsCheck, tcpCheck}
}

func (runner *agentDiagnosticRunner) diagnoseTLS(proxies []desiredProxy) []diagnostic.Check {
	tlsProxies := make([]desiredProxy, 0)
	for _, proxy := range proxies {
		if proxy.Security == "tls" {
			tlsProxies = append(tlsProxies, proxy)
		}
	}
	if len(tlsProxies) == 0 {
		return nil
	}
	certificates, err := readCurrentXrayCertificates(runner.xray.configPath)
	checks := make([]diagnostic.Check, 0, len(tlsProxies))
	for _, proxy := range tlsProxies {
		id := proxy.ID
		check := diagnostic.Check{
			Code: "tls.certificate", Status: diagnostic.StatusFail, ResourceID: &id,
			Label: proxy.ServerName,
		}
		if err != nil {
			check.Detail = "无法读取当前 Xray TLS 配置"
			checks = append(checks, check)
			continue
		}
		certificatePEM, exists := certificates[proxy.ID]
		if !exists {
			check.Detail = "当前 Xray 配置中未找到 TLS 证书"
			checks = append(checks, check)
			continue
		}
		block, _ := pem.Decode(certificatePEM)
		if block == nil || block.Type != "CERTIFICATE" {
			check.Detail = "TLS 证书无法解析"
			checks = append(checks, check)
			continue
		}
		certificate, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			check.Detail = "TLS 证书无法解析"
			checks = append(checks, check)
			continue
		}
		expiresAt := certificate.NotAfter.Unix()
		check.ExpiresAt = &expiresAt
		now := runner.now()
		switch {
		case now.Before(certificate.NotBefore):
			check.Detail = "TLS 证书尚未生效"
		case !now.Before(certificate.NotAfter):
			check.Detail = "TLS 证书已过期"
		case certificate.VerifyHostname(proxy.ServerName) != nil:
			check.Detail = "TLS 证书与配置域名不匹配"
		default:
			remainingDays := int64(certificate.NotAfter.Sub(now) / (24 * time.Hour))
			check.RemainingDays = &remainingDays
			check.Status = diagnostic.StatusPass
			check.Detail = fmt.Sprintf("TLS 证书有效，剩余 %d 天", remainingDays)
			if remainingDays < 30 {
				check.Status = diagnostic.StatusWarning
			}
		}
		checks = append(checks, check)
	}
	return checks
}

func serviceDiagnosticCheck(code, name string, active bool, err error) diagnostic.Check {
	check := diagnostic.Check{Code: code, Status: diagnostic.StatusPass, Detail: name + " service 正在运行"}
	if err != nil {
		check.Status = diagnostic.StatusFail
		check.Detail = safeDiagnosticError("无法读取 "+name+" service 状态", err)
	} else if !active {
		check.Status = diagnostic.StatusFail
		check.Detail = name + " service 未运行"
	}
	return check
}

func readCurrentXrayCertificates(configPath string) (map[int64][]byte, error) {
	value, err := readDiagnosticFile(configPath)
	if err != nil {
		return nil, err
	}
	var config struct {
		Inbounds []struct {
			Tag            string `json:"tag"`
			StreamSettings *struct {
				TLSSettings *struct {
					Certificates []struct {
						Certificate     []string `json:"certificate"`
						CertificateFile string   `json:"certificateFile"`
					} `json:"certificates"`
				} `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(value, &config); err != nil {
		return nil, err
	}
	result := make(map[int64][]byte)
	for _, inbound := range config.Inbounds {
		if !strings.HasPrefix(inbound.Tag, "proxy-") || inbound.StreamSettings == nil ||
			inbound.StreamSettings.TLSSettings == nil || len(inbound.StreamSettings.TLSSettings.Certificates) == 0 {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(inbound.Tag, "proxy-"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		certificate := inbound.StreamSettings.TLSSettings.Certificates[0]
		if certificate.CertificateFile != "" {
			value, err := readDiagnosticFile(certificate.CertificateFile)
			if err != nil {
				continue
			}
			result[id] = value
		} else if len(certificate.Certificate) != 0 {
			result[id] = []byte(strings.Join(certificate.Certificate, "\n") + "\n")
		}
	}
	return result, nil
}

func readDiagnosticFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maxDiagnosticFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(value) > maxDiagnosticFileBytes {
		return nil, errors.New("diagnostic file is too large")
	}
	return value, nil
}

func diagnosticAddressSummary(addresses []net.IPAddr) string {
	values := make([]string, 0, min(4, len(addresses)))
	for _, address := range addresses {
		value := address.IP.String()
		if value == "" || slicesContains(values, value) {
			continue
		}
		values = append(values, value)
		if len(values) == 4 {
			break
		}
	}
	return strings.Join(values, "、")
}

func slicesContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func safeDiagnosticError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	message := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(message), "timeout") {
		message = "timeout"
	}
	return diagnostic.SafeText(prefix+": "+message, diagnostic.MaxDetailBytes)
}
