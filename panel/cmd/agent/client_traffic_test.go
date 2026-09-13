package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseXrayClientStatsKeepsClientsIndependent(t *testing.T) {
	clients, err := parseXrayClientStats([]byte(`{
		"stat":[
			{"name":"user>>>vp-client-12>>>traffic>>>downlink","value":"4096"},
			{"name":"user>>>vp-client-8>>>traffic>>>uplink","value":1024},
			{"name":"user>>>vp-client-12>>>traffic>>>uplink","value":"2048"},
			{"name":"user>>>vp-client-8>>>traffic>>>downlink","value":512}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []agentClientTrafficItem{
		{ClientID: 8, UplinkBytes: 1024, DownlinkBytes: 512},
		{ClientID: 12, UplinkBytes: 2048, DownlinkBytes: 4096},
	}
	if !reflect.DeepEqual(clients, want) {
		t.Fatalf("parsed client traffic = %+v, want %+v", clients, want)
	}
}

func TestParseXrayClientStatsRejectsInvalidCounters(t *testing.T) {
	for name, output := range map[string]string{
		"bad identifier": `{"stat":[{"name":"user>>>vp-client-secret>>>traffic>>>uplink","value":"1"}]}`,
		"bad direction":  `{"stat":[{"name":"user>>>vp-client-1>>>traffic>>>total","value":"1"}]}`,
		"negative":       `{"stat":[{"name":"user>>>vp-client-1>>>traffic>>>uplink","value":"-1"}]}`,
		"duplicate":      `{"stat":[{"name":"user>>>vp-client-1>>>traffic>>>uplink","value":"1"},{"name":"user>>>vp-client-1>>>traffic>>>uplink","value":"2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseXrayClientStats([]byte(output)); err == nil {
				t.Fatal("invalid Xray stats were accepted")
			}
		})
	}
}

func TestParseXrayClientStatsDoesNotReportPartialClient(t *testing.T) {
	clients, err := parseXrayClientStats([]byte(`{"stat":[
		{"name":"user>>>vp-client-1>>>traffic>>>uplink","value":"7"},
		{"name":"inbound>>>proxy-1>>>traffic>>>uplink","value":"99"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("partial client counters were reported: %+v", clients)
	}
}

func TestXrayStatsCollectorUsesCumulativeStatsQuery(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := &xrayStatsCollector{
		binaryPath: "xray-test", configPath: configPath, apiAddress: "127.0.0.1:10085",
		runCommand: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			if name != "xray-test" || !reflect.DeepEqual(arguments, []string{
				"api", "statsquery", "--server=127.0.0.1:10085", "-pattern", xrayClientStatsPrefix,
			}) {
				t.Fatalf("stats query = %q %q", name, arguments)
			}
			for _, argument := range arguments {
				if argument == "-reset" || argument == "--reset" {
					t.Fatal("stats query resets cumulative counters")
				}
			}
			return []byte(`{"stat":[{"name":"user>>>vp-client-3>>>traffic>>>uplink","value":"1"},{"name":"user>>>vp-client-3>>>traffic>>>downlink","value":"2"}]}`), nil
		},
	}
	clients, err := collector.collect(t.Context())
	if err != nil || len(clients) != 1 || clients[0].ClientID != 3 {
		t.Fatalf("collect = (%+v, %v)", clients, err)
	}
}

func TestXrayStatsCollectorSkipsMissingManagedConfig(t *testing.T) {
	collector := &xrayStatsCollector{
		configPath: filepath.Join(t.TempDir(), "missing.json"),
		runCommand: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("Xray command ran without a managed config")
			return nil, nil
		},
	}
	clients, err := collector.collect(t.Context())
	if err != nil || len(clients) != 0 {
		t.Fatalf("collect without config = (%+v, %v)", clients, err)
	}
}

func TestClientTrafficReporterUsesAgentAuthentication(t *testing.T) {
	var received agentClientTrafficPayload
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent/traffic" ||
			r.Header.Get("Authorization") != "Bearer agent-secret" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request = %s %s, headers = %v", r.Method, r.URL.Path, r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer panel.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	reporter := &clientTrafficReporter{
		config: config{PanelURL: panel.URL, AgentToken: "agent-secret"}, client: panel.Client(),
		collector: &xrayStatsCollector{
			configPath: configPath,
			runCommand: func(context.Context, string, ...string) ([]byte, error) {
				return []byte(`{"stat":[{"name":"user>>>vp-client-5>>>traffic>>>uplink","value":"10"},{"name":"user>>>vp-client-5>>>traffic>>>downlink","value":"20"}]}`), nil
			},
		},
	}
	if err := reporter.report(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received.Clients, []agentClientTrafficItem{{ClientID: 5, UplinkBytes: 10, DownlinkBytes: 20}}) {
		t.Fatalf("reported traffic = %+v", received.Clients)
	}
}

func TestClientTrafficReporterPropagatesCollectionFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	reporter := &clientTrafficReporter{collector: &xrayStatsCollector{
		configPath: configPath,
		runCommand: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("stats unavailable")
		},
	}}
	if err := reporter.report(t.Context()); err == nil {
		t.Fatal("collection failure was ignored")
	}
}
