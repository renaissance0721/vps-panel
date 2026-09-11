package main

import (
	"errors"
	"math"
	"testing"
)

func TestParseCPUSample(t *testing.T) {
	sample, err := parseCPUSample([]byte("cpu  100 20 30 400 50 5 6 7 8 9\ncpu0 1 2 3 4\n"))
	if err != nil {
		t.Fatalf("parseCPUSample() error = %v", err)
	}
	if sample.Total != 618 || sample.Idle != 450 {
		t.Fatalf("parseCPUSample() = %+v, want total 618 idle 450", sample)
	}
}

func TestCalculateCPUPercentFromSamples(t *testing.T) {
	percent, ok := calculateCPUPercent(
		cpuSample{Total: 100, Idle: 60},
		cpuSample{Total: 200, Idle: 100},
	)
	if !ok || math.Abs(percent-60) > 0.001 {
		t.Fatalf("calculateCPUPercent() = (%f, %t), want 60%%", percent, ok)
	}
}

func TestCalculateCPUPercentRejectsInvalidDeltaWithoutNaNOrInf(t *testing.T) {
	for _, test := range []struct {
		name     string
		previous cpuSample
		current  cpuSample
	}{
		{name: "no total change", previous: cpuSample{Total: 100, Idle: 60}, current: cpuSample{Total: 100, Idle: 60}},
		{name: "total decreased", previous: cpuSample{Total: 100, Idle: 60}, current: cpuSample{Total: 90, Idle: 60}},
		{name: "idle decreased", previous: cpuSample{Total: 100, Idle: 60}, current: cpuSample{Total: 200, Idle: 50}},
		{name: "idle exceeds total delta", previous: cpuSample{Total: 100, Idle: 10}, current: cpuSample{Total: 110, Idle: 30}},
	} {
		t.Run(test.name, func(t *testing.T) {
			percent, ok := calculateCPUPercent(test.previous, test.current)
			if ok || math.IsNaN(percent) || math.IsInf(percent, 0) {
				t.Fatalf("calculateCPUPercent() = (%f, %t), want rejected finite value", percent, ok)
			}
		})
	}
}

func TestCalculateCPUPercentStaysWithinBounds(t *testing.T) {
	for _, test := range []struct {
		current cpuSample
		want    float64
	}{
		{current: cpuSample{Total: 200, Idle: 50}, want: 100},
		{current: cpuSample{Total: 200, Idle: 150}, want: 0},
	} {
		percent, ok := calculateCPUPercent(cpuSample{Total: 100, Idle: 50}, test.current)
		if !ok || percent != test.want || percent < 0 || percent > 100 {
			t.Fatalf("calculateCPUPercent() = (%f, %t), want %f", percent, ok, test.want)
		}
	}
}

func TestParseMemoryUsageUsesMemAvailable(t *testing.T) {
	used, total, err := parseMemoryUsage([]byte(
		"MemTotal:        2048 kB\nMemFree:          128 kB\nMemAvailable:     512 kB\n",
	))
	if err != nil {
		t.Fatalf("parseMemoryUsage() error = %v", err)
	}
	if used != 1536*1024 || total != 2048*1024 {
		t.Fatalf("parseMemoryUsage() = (%d, %d), want (%d, %d)", used, total, 1536*1024, 2048*1024)
	}
}

func TestReadUptime(t *testing.T) {
	uptime, err := readUptime(func(path string) ([]byte, error) {
		if path != "/proc/uptime" {
			t.Fatalf("read path = %q, want /proc/uptime", path)
		}
		return []byte("86400.75 100.00\n"), nil
	})
	if err != nil || uptime != 86400 {
		t.Fatalf("readUptime() = (%d, %v), want 86400", uptime, err)
	}
}

func TestParseDefaultRouteInterfaceExcludesLoopbackAndUsesLowestMetric(t *testing.T) {
	data := []byte("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"lo\t00000000\t00000000\t0001\t0\t0\t0\t00000000\n" +
		"eth1\t00000000\t01010101\t0003\t0\t0\t200\t00000000\n" +
		"eth0\t00000000\t01010101\t0003\t0\t0\t100\t00000000\n")
	name, err := parseDefaultRouteInterface(data)
	if err != nil || name != "eth0" {
		t.Fatalf("parseDefaultRouteInterface() = (%q, %v), want eth0", name, err)
	}
}

func TestParseNetworkUsageReadsSelectedInterface(t *testing.T) {
	data := []byte("Inter-| Receive | Transmit\n" +
		" lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0\n" +
		"eth0: 123456 1 2 3 4 5 6 7 987654 8 9 10 11 12 13 14\n")
	rx, tx, err := parseNetworkUsage(data, "eth0")
	if err != nil || rx != 123456 || tx != 987654 {
		t.Fatalf("parseNetworkUsage() = (%d, %d, %v), want (123456, 987654, nil)", rx, tx, err)
	}
}

func TestMetricsCollectorBuildsCompleteMessage(t *testing.T) {
	collector := &metricsCollector{
		previousCPU: &cpuSample{Total: 100, Idle: 50},
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "/proc/stat":
				return []byte("cpu 80 0 20 80 20 0 0 0\n"), nil
			case "/proc/meminfo":
				return []byte("MemTotal: 4096 kB\nMemAvailable: 1024 kB\n"), nil
			case "/proc/uptime":
				return []byte("3661.9 10.0\n"), nil
			case "/proc/net/route":
				return []byte("Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 00000000 01010101 0003 0 0 100 00000000\n"), nil
			case "/proc/net/dev":
				return []byte("eth0: 123456 0 0 0 0 0 0 0 987654 0 0 0 0 0 0 0\n"), nil
			default:
				return nil, errors.New("unexpected path")
			}
		},
		diskUsage: func(path string) (int64, int64, error) {
			if path != "/" {
				t.Fatalf("disk path = %q, want /", path)
			}
			return 5 << 30, 10 << 30, nil
		},
	}
	message, ok := collector.collect()
	if !ok {
		t.Fatal("metricsCollector.collect() rejected complete metrics")
	}
	if message.Type != "metrics" || message.CPUPercent != 50 ||
		message.MemoryUsedBytes != 3<<20 || message.MemoryTotalBytes != 4<<20 ||
		message.DiskUsedBytes != 5<<30 || message.DiskTotalBytes != 10<<30 ||
		message.UptimeSeconds != 3661 || message.NICRXBytes != 123456 || message.NICTXBytes != 987654 {
		t.Fatalf("metrics message = %+v", message)
	}
}

func TestMetricsCollectorFailureSkipsSample(t *testing.T) {
	collector := &metricsCollector{
		previousCPU: &cpuSample{Total: 100, Idle: 50},
		readFile: func(path string) ([]byte, error) {
			if path == "/proc/stat" {
				return []byte("cpu 80 0 20 80 20 0 0 0\n"), nil
			}
			return nil, errors.New("unavailable")
		},
		diskUsage: func(string) (int64, int64, error) {
			return 0, 0, errors.New("unavailable")
		},
	}
	if _, ok := collector.collect(); ok {
		t.Fatal("metricsCollector.collect() accepted an incomplete sample")
	}
}
