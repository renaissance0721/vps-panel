package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/coder/websocket"
)

const maxMetricInteger = uint64(1<<63 - 1)

type metricsMessage struct {
	Type             string  `json:"type"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskUsedBytes    int64   `json:"disk_used_bytes"`
	DiskTotalBytes   int64   `json:"disk_total_bytes"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
}

type cpuSample struct {
	Total uint64
	Idle  uint64
}

type metricsCollector struct {
	readFile    func(string) ([]byte, error)
	diskUsage   func(string) (int64, int64, error)
	previousCPU *cpuSample
}

func newMetricsCollector() *metricsCollector {
	collector := &metricsCollector{
		readFile:  os.ReadFile,
		diskUsage: readRootDiskUsage,
	}
	if sample, err := readCPUSample(collector.readFile); err == nil {
		collector.previousCPU = &sample
	}
	return collector
}

func (collector *metricsCollector) collect() (metricsMessage, bool) {
	currentCPU, err := readCPUSample(collector.readFile)
	if err != nil {
		return metricsMessage{}, false
	}
	previousCPU := collector.previousCPU
	collector.previousCPU = &currentCPU
	if previousCPU == nil {
		return metricsMessage{}, false
	}
	cpuPercent, ok := calculateCPUPercent(*previousCPU, currentCPU)
	if !ok {
		return metricsMessage{}, false
	}

	memoryUsed, memoryTotal, err := readMemoryUsage(collector.readFile)
	if err != nil {
		return metricsMessage{}, false
	}
	diskUsed, diskTotal, err := collector.diskUsage("/")
	if err != nil {
		return metricsMessage{}, false
	}
	uptime, err := readUptime(collector.readFile)
	if err != nil {
		return metricsMessage{}, false
	}

	return metricsMessage{
		Type:             "metrics",
		CPUPercent:       cpuPercent,
		MemoryUsedBytes:  memoryUsed,
		MemoryTotalBytes: memoryTotal,
		DiskUsedBytes:    diskUsed,
		DiskTotalBytes:   diskTotal,
		UptimeSeconds:    uptime,
	}, true
}

func readCPUSample(readFile func(string) ([]byte, error)) (cpuSample, error) {
	data, err := readFile("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	return parseCPUSample(data)
}

func parseCPUSample(data []byte) (cpuSample, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}
		if len(fields) < 5 {
			return cpuSample{}, errors.New("invalid /proc/stat CPU line")
		}
		valueCount := len(fields) - 1
		if valueCount > 8 {
			valueCount = 8
		}
		values := make([]uint64, valueCount)
		var total uint64
		for index := 0; index < valueCount; index++ {
			value, err := strconv.ParseUint(fields[index+1], 10, 64)
			if err != nil || ^uint64(0)-total < value {
				return cpuSample{}, errors.New("invalid /proc/stat CPU value")
			}
			values[index] = value
			total += value
		}
		idle := values[3]
		if len(values) > 4 {
			if ^uint64(0)-idle < values[4] {
				return cpuSample{}, errors.New("invalid /proc/stat idle value")
			}
			idle += values[4]
		}
		return cpuSample{Total: total, Idle: idle}, nil
	}
	return cpuSample{}, errors.New("missing /proc/stat CPU line")
}

func calculateCPUPercent(previous, current cpuSample) (float64, bool) {
	if current.Total <= previous.Total || current.Idle < previous.Idle {
		return 0, false
	}
	deltaTotal := current.Total - previous.Total
	deltaIdle := current.Idle - previous.Idle
	if deltaIdle > deltaTotal {
		return 0, false
	}
	percent := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return 0, false
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

func readMemoryUsage(readFile func(string) ([]byte, error)) (int64, int64, error) {
	data, err := readFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	return parseMemoryUsage(data)
}

func parseMemoryUsage(data []byte) (int64, int64, error) {
	var totalKiB, availableKiB uint64
	var totalFound, availableFound bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, errors.New("invalid /proc/meminfo value")
		}
		switch fields[0] {
		case "MemTotal:":
			totalKiB = value
			totalFound = true
		case "MemAvailable:":
			availableKiB = value
			availableFound = true
		}
	}
	if !totalFound || !availableFound || availableKiB > totalKiB || totalKiB > maxMetricInteger/1024 {
		return 0, 0, errors.New("invalid /proc/meminfo memory values")
	}
	total := totalKiB * 1024
	available := availableKiB * 1024
	return int64(total - available), int64(total), nil
}

func readUptime(readFile func(string) ([]byte, error)) (int64, error) {
	data, err := readFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("invalid /proc/uptime")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(maxMetricInteger) {
		return 0, errors.New("invalid /proc/uptime value")
	}
	return int64(seconds), nil
}

func sendMetrics(ctx context.Context, connection *websocket.Conn, message metricsMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}
