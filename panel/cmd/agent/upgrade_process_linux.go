//go:build linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startDetachedUpgradeProcess(executable string, arguments ...string) error {
	command := exec.Command(executable, arguments...)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
