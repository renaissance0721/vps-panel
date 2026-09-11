//go:build !linux

package main

import "errors"

func readRootDiskUsage(string) (int64, int64, error) {
	return 0, 0, errors.New("filesystem metrics require Linux")
}
