//go:build linux

package main

import "testing"

func TestReadRootDiskUsage(t *testing.T) {
	used, total, err := readRootDiskUsage("/")
	if err != nil {
		t.Fatalf("readRootDiskUsage() error = %v", err)
	}
	if total <= 0 || used < 0 || used > total {
		t.Fatalf("readRootDiskUsage() = (%d, %d), want valid usage", used, total)
	}
}
