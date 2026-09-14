package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type initSystem string

const (
	initSystemSystemd initSystem = "systemd"
	initSystemOpenRC  initSystem = "openrc"
)

type libcKind string

const (
	libcGlibc libcKind = "glibc"
	libcMusl  libcKind = "musl"
)

var errUnsupportedInitSystem = errors.New("unsupported init system: systemd or OpenRC is required")

type hostEnvironment struct {
	InitSystem initSystem
	Libc       libcKind
	OSName     string
	OSVersion  string
}

type hostEnvironmentDependencies struct {
	goos       string
	lookPath   func(string) (string, error)
	pathExists func(string) (bool, error)
	readFile   func(string) ([]byte, error)
	glob       func(string) ([]string, error)
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

func detectHostEnvironment() (hostEnvironment, error) {
	return detectHostEnvironmentWith(hostEnvironmentDependencies{
		goos:       runtime.GOOS,
		lookPath:   exec.LookPath,
		pathExists: pathExists,
		readFile:   os.ReadFile,
		glob:       filepath.Glob,
		runCommand: runXrayCommand,
	})
}

func detectHostEnvironmentWith(dependencies hostEnvironmentDependencies) (hostEnvironment, error) {
	if dependencies.goos != "linux" {
		return hostEnvironment{}, fmt.Errorf("unsupported operating system: %s", dependencies.goos)
	}

	environment := hostEnvironment{Libc: libcGlibc, OSName: "Linux"}
	osRelease, _ := dependencies.readFile("/etc/os-release")
	if name, version := parseOSRelease(osRelease); name != "" || version != "" {
		if name != "" {
			environment.OSName = name
		}
		environment.OSVersion = version
	}

	musl := false
	if loaders, _ := dependencies.glob("/lib/ld-musl-*.so.1"); len(loaders) != 0 {
		musl = true
	} else if dependencies.runCommand != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, _ := dependencies.runCommand(ctx, "ldd", "--version")
		cancel()
		if strings.Contains(strings.ToLower(string(output)), "musl") {
			musl = true
		}
	}
	if !musl && strings.Contains(strings.ToLower(string(osRelease)), "alpine") {
		musl = true
	}
	if musl {
		environment.Libc = libcMusl
	}

	systemdRuntime, systemdErr := dependencies.pathExists("/run/systemd/system")
	_, systemctlErr := dependencies.lookPath("systemctl")
	if systemdErr == nil && systemdRuntime && systemctlErr == nil {
		environment.InitSystem = initSystemSystemd
		return environment, nil
	}

	_, rcServiceErr := dependencies.lookPath("rc-service")
	_, rcUpdateErr := dependencies.lookPath("rc-update")
	openRCRuntime, openRCErr := dependencies.pathExists("/run/openrc")
	openRCRun, openRCRunErr := dependencies.pathExists("/sbin/openrc-run")
	if rcServiceErr == nil && rcUpdateErr == nil &&
		((openRCErr == nil && openRCRuntime) || (openRCRunErr == nil && openRCRun)) {
		environment.InitSystem = initSystemOpenRC
		return environment, nil
	}

	return environment, errUnsupportedInitSystem
}
