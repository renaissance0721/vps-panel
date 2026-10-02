package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
)

func testProbeTask(id int64) monitor.ProbeTask {
	port := 443
	return monitor.ProbeTask{ID: id, Name: "test", Type: "tcp", Target: "127.0.0.1", Port: &port, IntervalSeconds: 60}
}

func TestTCPProbeIPv4IPv6AndRefused(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Skipf("loopback unavailable: %v", err)
			}
			defer listener.Close()
			accepted := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					defer conn.Close()
					conn.SetReadDeadline(time.Now().Add(time.Second))
					var data [1]byte
					n, readErr := conn.Read(data[:])
					if n != 0 || readErr == nil {
						err = errors.New("TCP probe sent application data")
					}
				}
				accepted <- err
			}()
			host, rawPort, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(rawPort)
			task := testProbeTask(1)
			task.Target = host
			task.Port = &port
			runner := newProbeRunner()
			runner.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				t.Error("literal IP should bypass DNS")
				return nil, nil
			}
			result := runner.run(t.Context(), task)
			if result.Outcome != "success" || result.LatencyMS == nil || *result.LatencyMS < 0 || *result.LatencyMS >= 900 {
				t.Fatalf("result = %+v", result)
			}
			if err := <-accepted; err != nil {
				t.Fatal(err)
			}
			listener.Close()
			result = runner.run(t.Context(), task)
			if result.Outcome != "connect_error" || result.LatencyMS != nil {
				t.Fatalf("refused = %+v", result)
			}
		})
	}
}

func TestTCPProbeTimeoutDNSCancellationAndAddressLimit(t *testing.T) {
	task := testProbeTask(1)
	task.Target = "example.test"
	runner := newProbeRunner()
	runner.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "not found", IsNotFound: true}
	}
	if result := runner.run(t.Context(), task); result.Outcome != "dns_error" || result.LatencyMS != nil {
		t.Fatalf("DNS = %+v", result)
	}
	runner.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("192.0.2.2")}, {IP: net.ParseIP("192.0.2.3")}}, nil
	}
	var attempts atomic.Int32
	runner.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		attempts.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	started := time.Now()
	result := runner.run(t.Context(), task)
	if result.Outcome != "timeout" || result.LatencyMS != nil || attempts.Load() != 3 || time.Since(started) > 2*time.Second {
		t.Fatalf("timeout = %+v, attempts = %d", result, attempts.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := runner.run(ctx, task); result.Outcome != "cancelled" || result.LatencyMS != nil {
		t.Fatalf("cancelled = %+v", result)
	}
}

func TestTCPProbeMultiAddressSuccessExcludesDNS(t *testing.T) {
	runner := newProbeRunner()
	runner.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		time.Sleep(200 * time.Millisecond)
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("2001:db8::1")}}, nil
	}
	runner.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "192.0.2.1:443" {
			return nil, syscall.ECONNREFUSED
		}
		left, right := net.Pipe()
		right.Close()
		return left, nil
	}
	task := testProbeTask(1)
	task.Target = "example.test"
	result := runner.run(t.Context(), task)
	if result.Outcome != "success" || result.LatencyMS == nil || *result.LatencyMS >= 150 {
		t.Fatalf("DNS included or second address ignored: %+v", result)
	}
}

func TestICMPProbePermissionTimeoutAndCancellation(t *testing.T) {
	task := testProbeTask(1)
	task.Type = "icmp"
	task.Port = nil
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", &net.OpError{Op: "listen", Err: syscall.EPERM}, "permission_error"},
		{"timeout", nil, "timeout"},
		{"deadline", context.DeadlineExceeded, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := newProbeRunner()
			runner.runPing = func(_ context.Context, p *ping.Pinger) error {
				if p.Count != 1 || p.Timeout != time.Second {
					t.Fatalf("unsafe Echo count or timeout: %+v", p)
				}
				return tc.err
			}
			result := runner.run(t.Context(), task)
			if result.Outcome != tc.want || result.LatencyMS != nil {
				t.Fatalf("ICMP = %+v", result)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	runner := newProbeRunner()
	runner.runPing = func(ctx context.Context, _ *ping.Pinger) error { cancel(); return ctx.Err() }
	if result := runner.run(ctx, task); result.Outcome != "cancelled" {
		t.Fatalf("ICMP cancellation = %+v", result)
	}
}

func TestProbeManagerReconcilesAndCancelsWorkers(t *testing.T) {
	m := newProbeManager(t.Context())
	defer m.close()
	started := make(chan int64, 70)
	var cancelled atomic.Int32
	m.run = func(ctx context.Context, task monitor.ProbeTask) monitor.ProbeResult {
		started <- task.ID
		<-ctx.Done()
		cancelled.Add(1)
		return monitor.ProbeResult{TaskID: task.ID, Outcome: "cancelled"}
	}
	apply := func(version int64, tasks ...monitor.ProbeTask) {
		t.Helper()
		if err := m.apply(monitor.DesiredTasks{Version: version, Tasks: tasks}); err != nil {
			t.Fatal(err)
		}
	}
	task := testProbeTask(1)
	apply(1, task)
	<-started
	first := m.workers[1]
	// Same value with a different port pointer must not restart the timer.
	apply(2, testProbeTask(1))
	if m.workers[1] != first || cancelled.Load() != 0 {
		t.Fatal("unchanged task restarted")
	}
	task.Target = "::1"
	apply(3, task, testProbeTask(2))
	<-started
	<-started
	if m.workers[1] == first || len(m.workers) != 2 || cancelled.Load() != 1 {
		t.Fatal("update/add not reconciled")
	}
	if m.current(completedProbe{worker: first, result: monitor.ProbeResult{TaskID: 1}}) {
		t.Fatal("old worker result accepted")
	}
	apply(4, task)
	if len(m.workers) != 1 || cancelled.Load() != 2 {
		t.Fatal("delete not cancelled")
	}
	apply(3)
	if len(m.workers) != 1 {
		t.Fatal("stale list applied")
	}
	m.close()
	if len(m.workers) != 0 || cancelled.Load() != 3 {
		t.Fatal("disconnect left workers alive")
	}
}

func TestProbeManagerHardLimitsAreAtomic(t *testing.T) {
	m := newProbeManager(t.Context())
	defer m.close()
	m.run = func(ctx context.Context, task monitor.ProbeTask) monitor.ProbeResult {
		<-ctx.Done()
		return monitor.ProbeResult{}
	}
	tasks := []monitor.ProbeTask{}
	for i := int64(1); i <= maxProbeTasks; i++ {
		tasks = append(tasks, testProbeTask(i))
	}
	if err := m.apply(monitor.DesiredTasks{Version: 1, Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
	if err := m.apply(monitor.DesiredTasks{Version: 2, Tasks: append(tasks, testProbeTask(65))}); !errors.Is(err, monitor.ErrTaskLimit) {
		t.Fatalf("limit: %v", err)
	}
	bad := testProbeTask(1)
	bad.IntervalSeconds = 4
	if err := m.apply(monitor.DesiredTasks{Version: 2, Tasks: []monitor.ProbeTask{bad}}); err == nil {
		t.Fatal("unsafe interval accepted")
	}
	if len(m.workers) != 64 || m.version != 1 {
		t.Fatal("invalid list changed running tasks")
	}
}
