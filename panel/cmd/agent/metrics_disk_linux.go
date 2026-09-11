//go:build linux

package main

import (
	"errors"
	"syscall"
)

func readRootDiskUsage(path string) (int64, int64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, 0, err
	}
	if stats.Bsize <= 0 || stats.Blocks > maxMetricInteger/uint64(stats.Bsize) ||
		stats.Bfree > maxMetricInteger/uint64(stats.Bsize) {
		return 0, 0, errors.New("invalid filesystem statistics")
	}
	total := stats.Blocks * uint64(stats.Bsize)
	free := stats.Bfree * uint64(stats.Bsize)
	if free > total {
		return 0, 0, errors.New("invalid filesystem free space")
	}
	return int64(total - free), int64(total), nil
}
