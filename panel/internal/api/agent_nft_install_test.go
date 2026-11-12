package api

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func nftTestShell(t *testing.T) string {
	t.Helper()
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{`C:\Program Files\Git\bin\sh.exe`, `C:\msys64\usr\bin\sh.exe`} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	t.Skip("POSIX sh is unavailable")
	return ""
}

func TestAgentInstallAndUpgradePrepareNFTables(t *testing.T) {
	sh := nftTestShell(t)
	for _, script := range []struct{ name, source string }{{"install", string(installAgentScript)}, {"upgrade", upgradeAgentScript}} {
		t.Run(script.name, func(t *testing.T) {
			function := upgradeScriptSection(t, script.source, "prepare_nftables_dependency() {", "\ndetect_init_system() {")
			function = strings.Replace(function, `if [ -r /etc/os-release ]; then . /etc/os-release; fi`, `ID="$TEST_OS"; ID_LIKE="$TEST_LIKE"`, 1)
			function = strings.Replace(function, `[ -f /etc/alpine-release ]`, `[ "${TEST_ALPINE:-}" = yes ]`, 1)
			if strings.Contains(function, "/etc/os-release") || strings.Contains(function, "/etc/alpine-release") {
				t.Fatal("host OS probes not isolated")
			}
			for _, tc := range []struct {
				name, os, like, installed, failure string
				want                               []string
				fail                               bool
			}{
				{"alpine missing", "alpine", "", "no", "", []string{"apk add --no-cache nftables"}, false},
				{"alpine installed", "alpine", "", "yes", "", nil, false},
				{"debian missing", "debian", "", "no", "", []string{"apt-get update", "apt-get install -y nftables"}, false},
				{"ubuntu missing", "ubuntu", "", "no", "", []string{"apt-get update", "apt-get install -y nftables"}, false},
				{"debian derivative", "example", "debian", "no", "", []string{"apt-get update", "apt-get install -y nftables"}, false},
				{"unknown", "unknown", "", "no", "", []string{"unsupported distribution"}, false},
				{"apk failure", "alpine", "", "no", "apk", []string{"failed to install nftables with apk"}, true},
				{"apt update failure", "debian", "", "no", "update", []string{"failed to update apt package indexes"}, true},
				{"apt install failure", "debian", "", "no", "install", []string{"failed to install nftables with apt-get"}, true},
				{"missing binary", "alpine", "", "no", "missing", []string{"nft was not found"}, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					preamble := `set -eu
PATH=''
log() { printf 'log %s\n' "$*"; }
fail() { printf 'error %s\n' "$*"; exit 1; }
if [ "$TEST_INSTALLED" = yes ]; then nft() { :; }; fi
apk() { printf 'apk %s\n' "$*"; [ "$TEST_FAIL" != apk ] || return 1; if [ "$TEST_FAIL" != missing ]; then nft() { :; }; fi; }
apt-get() { printf 'apt-get %s\n' "$*"; [ "$TEST_FAIL" != "$1" ] || return 1; if [ "$1" = install ]; then nft() { :; }; fi; }
`
					command := exec.Command(sh, "-s")
					command.Stdin = strings.NewReader(preamble + function + "\nprepare_nftables_dependency\nprepare_nftables_dependency\n")
					command.Env = append(os.Environ(), "TEST_OS="+tc.os, "TEST_LIKE="+tc.like, "TEST_INSTALLED="+tc.installed, "TEST_FAIL="+tc.failure, "TEST_ALPINE=no")
					out, err := command.CombinedOutput()
					if (err != nil) != tc.fail {
						t.Fatalf("exit %v: %s", err, out)
					}
					for _, want := range tc.want {
						if !strings.Contains(string(out), want) {
							t.Fatalf("missing %q: %s", want, out)
						}
					}
					if !tc.fail && tc.os != "unknown" && tc.installed != "yes" && strings.Count(string(out), "apk add --no-cache nftables")+strings.Count(string(out), "apt-get install -y nftables") != 1 {
						t.Fatalf("not idempotent: %s", out)
					}
					if (tc.installed == "yes" || tc.os == "unknown") && (strings.Contains(string(out), "apk add") || strings.Contains(string(out), "apt-get ")) {
						t.Fatalf("unexpected package manager: %s", out)
					}
				})
			}
		})
	}
}

func TestAgentNFTPreparationOccursBeforeRegistrationOrUpgrade(t *testing.T) {
	for _, script := range []struct{ name, source, validated, mutation string }{
		{"install", string(installAgentScript), "\ndetect_init_system\n", `"$download_path" register`},
		{"upgrade", upgradeAgentScript, `[ -s "$CONFIG_FILE" ]`, "\nchanged=true\n"},
	} {
		t.Run(script.name, func(t *testing.T) {
			call := "\nprepare_nftables_dependency\n"
			if strings.Count(script.source, call) != 1 {
				t.Fatal("dependency preparation must run exactly once")
			}
			check, prepare, mutate := strings.Index(script.source, script.validated), strings.Index(script.source, call), strings.Index(script.source, script.mutation)
			if check < 0 || prepare <= check || mutate <= prepare {
				t.Fatal("dependency preparation must follow validation and precede registration or upgrade mutation")
			}
			function := upgradeScriptSection(t, script.source, "prepare_nftables_dependency() {", "\ndetect_init_system() {")
			for _, forbidden := range []string{"systemctl", "rc-service", "flush", "nft -"} {
				if strings.Contains(function, forbidden) {
					t.Fatalf("dependency preparation must not change firewall or service state: %s", forbidden)
				}
			}
		})
	}
}
