package main

import (
	"context"
	"errors"
	"testing"
)

func TestDetectHostEnvironmentSelectsSystemdAndGlibc(t *testing.T) {
	environment, err := detectHostEnvironmentWith(testHostEnvironmentDependencies(
		map[string]bool{"/run/systemd/system": true},
		map[string]bool{"systemctl": true},
		[]byte("NAME=Debian GNU/Linux\nVERSION_ID=12\n"),
		nil,
	))
	if err != nil || environment.InitSystem != initSystemSystemd || environment.Libc != libcGlibc ||
		environment.OSName != "Debian GNU/Linux" || environment.OSVersion != "12" {
		t.Fatalf("systemd environment = (%+v, %v)", environment, err)
	}
}

func TestDetectHostEnvironmentSelectsOpenRCAndMusl(t *testing.T) {
	environment, err := detectHostEnvironmentWith(testHostEnvironmentDependencies(
		map[string]bool{"/run/openrc": true},
		map[string]bool{"rc-service": true, "rc-update": true},
		[]byte("NAME=\"Alpine Linux\"\nVERSION_ID=3.22\n"),
		[]string{"/lib/ld-musl-x86_64.so.1"},
	))
	if err != nil || environment.InitSystem != initSystemOpenRC || environment.Libc != libcMusl ||
		environment.OSName != "Alpine Linux" || environment.OSVersion != "3.22" {
		t.Fatalf("OpenRC environment = (%+v, %v)", environment, err)
	}
}

func TestDetectHostEnvironmentUsesAlpineAsMuslFallback(t *testing.T) {
	dependencies := testHostEnvironmentDependencies(
		map[string]bool{"/sbin/openrc-run": true},
		map[string]bool{"rc-service": true, "rc-update": true},
		[]byte("ID=alpine\nNAME=\"Alpine Linux\"\nVERSION_ID=3.21\n"),
		nil,
	)
	dependencies.runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("ldd unavailable")
	}
	environment, err := detectHostEnvironmentWith(dependencies)
	if err != nil || environment.InitSystem != initSystemOpenRC || environment.Libc != libcMusl {
		t.Fatalf("Alpine fallback environment = (%+v, %v)", environment, err)
	}
}

func TestDetectHostEnvironmentRejectsUnsupportedInitSystem(t *testing.T) {
	_, err := detectHostEnvironmentWith(testHostEnvironmentDependencies(nil, nil, nil, nil))
	if err == nil || err.Error() != "unsupported init system: systemd or OpenRC is required" {
		t.Fatalf("unsupported init error = %v", err)
	}
}

func testHostEnvironmentDependencies(
	paths map[string]bool,
	commands map[string]bool,
	osRelease []byte,
	muslLoaders []string,
) hostEnvironmentDependencies {
	return hostEnvironmentDependencies{
		goos: "linux",
		lookPath: func(name string) (string, error) {
			if commands[name] {
				return name, nil
			}
			return "", errors.New("not found")
		},
		pathExists: func(path string) (bool, error) { return paths[path], nil },
		readFile: func(path string) ([]byte, error) {
			if path == "/etc/os-release" && osRelease != nil {
				return osRelease, nil
			}
			return nil, errors.New("not found")
		},
		glob: func(string) ([]string, error) { return muslLoaders, nil },
		runCommand: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("not found")
		},
	}
}
