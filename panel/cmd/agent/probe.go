package main

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"strconv"
	"syscall"
	"time"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
)

const (
	maxProbeTasks    = 64
	minProbeInterval = 5 * time.Second
	tcpProbeTimeout  = 900 * time.Millisecond
	icmpProbeTimeout = time.Second
)

type probeRunner struct {
	lookupIP func(context.Context, string) ([]net.IPAddr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
	runPing  func(context.Context, *ping.Pinger) error
}

func newProbeRunner() *probeRunner {
	return &probeRunner{
		lookupIP: net.DefaultResolver.LookupIPAddr,
		dial:     (&net.Dialer{}).DialContext,
		runPing:  func(ctx context.Context, p *ping.Pinger) error { return p.RunWithContext(ctx) },
	}
}

func (p *probeRunner) run(ctx context.Context, task monitor.ProbeTask) monitor.ProbeResult {
	result := monitor.ProbeResult{Type: "probe_result", TaskID: task.ID}
	addresses := []net.IPAddr{}
	if ip := net.ParseIP(task.Target); ip != nil {
		addresses = append(addresses, net.IPAddr{IP: ip})
	} else {
		dnsContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		var err error
		addresses, err = p.lookupIP(dnsContext, task.Target)
		cancel()
		if ctx.Err() != nil {
			result.Outcome = "cancelled"
			return result
		}
		if err != nil || len(addresses) == 0 {
			result.Outcome = "dns_error"
			return result
		}
	}
	if ctx.Err() != nil {
		result.Outcome = "cancelled"
		return result
	}
	if task.Type == "icmp" {
		result.Outcome, result.LatencyMS = p.icmp(ctx, addresses[0])
	} else {
		result.Outcome, result.LatencyMS = p.tcp(ctx, addresses, *task.Port)
	}
	return result
}

func probeNetworkOutcome(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if os.IsPermission(err) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.Errno(10013)) {
		return "permission_error"
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "connect_error"
}

func (p *probeRunner) tcp(ctx context.Context, addresses []net.IPAddr, port int) (string, *float64) {
	// One bounded wave, not a retry after a failed measurement. DNS is already done.
	connectContext, cancel := context.WithTimeout(ctx, tcpProbeTimeout)
	defer cancel()
	type attempt struct {
		outcome string
		latency *float64
	}
	results := make(chan attempt, 3)
	seen := map[string]bool{}
	count := 0
	for _, ip := range addresses {
		address := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if seen[address] {
			continue
		}
		seen[address] = true
		count++
		go func() {
			started := time.Now()
			connection, err := p.dial(connectContext, "tcp", address)
			elapsed := time.Since(started)
			if connection != nil {
				connection.Close()
			}
			if err != nil {
				results <- attempt{outcome: probeNetworkOutcome(ctx, err)}
				return
			}
			if elapsed >= tcpProbeTimeout || connectContext.Err() != nil {
				results <- attempt{outcome: "timeout"}
				return
			}
			ms := float64(elapsed) / float64(time.Millisecond)
			results <- attempt{outcome: "success", latency: &ms}
		}()
		if count == 3 {
			break
		}
	}
	outcome := "connect_error"
	for range count {
		select {
		case <-ctx.Done():
			return "cancelled", nil
		case <-connectContext.Done():
			return "timeout", nil
		case result := <-results:
			if ctx.Err() != nil {
				return "cancelled", nil
			}
			if result.outcome == "success" {
				return result.outcome, result.latency
			}
			if result.outcome == "timeout" || outcome != "timeout" {
				outcome = result.outcome
			}
		}
	}
	return outcome, nil
}

func (p *probeRunner) icmp(ctx context.Context, address net.IPAddr) (string, *float64) {
	pinger := ping.New(address.String())
	pinger.SetIPAddr(&address)
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Timeout = icmpProbeTimeout
	pinger.RecordRtts = false
	err := p.runPing(ctx, pinger)
	if ctx.Err() != nil {
		return "cancelled", nil
	}
	if err != nil {
		return probeNetworkOutcome(ctx, err), nil
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return "timeout", nil
	}
	ms := float64(stats.AvgRtt) / float64(time.Millisecond)
	return "success", &ms
}

type probeWorker struct {
	task   monitor.ProbeTask
	cancel context.CancelFunc
	done   chan struct{}
}

type completedProbe struct {
	worker *probeWorker
	result monitor.ProbeResult
}

type probeManager struct {
	ctx     context.Context
	workers map[int64]*probeWorker
	results chan completedProbe
	run     func(context.Context, monitor.ProbeTask) monitor.ProbeResult
	version int64
}

func newProbeManager(ctx context.Context) *probeManager {
	return &probeManager{ctx: ctx, workers: map[int64]*probeWorker{}, results: make(chan completedProbe, maxProbeTasks), run: newProbeRunner().run}
}

func (m *probeManager) apply(list monitor.DesiredTasks) error {
	if len(list.Tasks) > maxProbeTasks {
		return monitor.ErrTaskLimit
	}
	desired := make(map[int64]monitor.ProbeTask, len(list.Tasks))
	for _, task := range list.Tasks {
		if task.ID <= 0 || time.Duration(task.IntervalSeconds)*time.Second < minProbeInterval {
			return monitor.ErrInvalid
		}
		if _, exists := desired[task.ID]; exists {
			return monitor.ErrInvalid
		}
		if err := monitor.ValidateTask(task); err != nil {
			return err
		}
		desired[task.ID] = task
	}
	if list.Version <= 0 {
		return monitor.ErrInvalid
	}
	if list.Version <= m.version {
		return nil
	}
	m.version = list.Version
	for id, worker := range m.workers {
		if task, exists := desired[id]; !exists || !reflect.DeepEqual(task, worker.task) {
			worker.cancel()
			<-worker.done
			delete(m.workers, id)
		}
	}
	for id, task := range desired {
		if m.workers[id] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(m.ctx)
		worker := &probeWorker{task: task, cancel: cancel, done: make(chan struct{})}
		m.workers[id] = worker
		go m.work(ctx, worker)
	}
	return nil
}

func (m *probeManager) work(ctx context.Context, worker *probeWorker) {
	defer close(worker.done)
	ticker := time.NewTicker(time.Duration(worker.task.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		result := m.run(ctx, worker.task)
		if ctx.Err() != nil {
			return
		}
		select {
		case m.results <- completedProbe{worker, result}:
		case <-ctx.Done():
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (m *probeManager) current(result completedProbe) bool {
	return m.workers[result.result.TaskID] == result.worker
}

func (m *probeManager) close() {
	for _, worker := range m.workers {
		worker.cancel()
	}
	for _, worker := range m.workers {
		<-worker.done
	}
	clear(m.workers)
}
