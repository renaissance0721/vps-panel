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
			for _, installed := range []bool{false, true} {
				preamble := "set -eu\nPATH=''\nlog() { printf '%s\\n' \"$*\"; }\n"
				if installed {
					preamble += "nft() { :; }\n"
				}
				command := exec.Command(sh, "-s")
				command.Stdin = strings.NewReader(preamble + function + "\nprepare_nftables_dependency\n")
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("installed=%t: %v: %s", installed, err, out)
				}
				if strings.Contains(string(out), "nftables is unavailable") == installed {
					t.Fatalf("installed=%t: unexpected output %s", installed, out)
				}
			}
			for _, forbidden := range []string{"apt-get", "apk add", "nft -", "flush"} {
				if strings.Contains(function, forbidden) {
					t.Fatalf("unexpected system change: %s", forbidden)
				}
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
