//go:build !linux

package main

import "errors"

func startDetachedUpgradeProcess(string, ...string) error {
	return errors.New("detached Agent upgrade is supported only on Linux")
}
