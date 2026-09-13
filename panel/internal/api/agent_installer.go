package api

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed install-agent.sh
var installAgentScript []byte

//go:embed upgrade-agent.sh
var upgradeAgentScript string

func (s *server) installAgent(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(installAgentScript)
}

func (s *server) upgradeAgentInstaller(w http.ResponseWriter, _ *http.Request) {
	version := formalReleaseVersion(s.panelVersion)
	if version == "" {
		writeError(w, http.StatusConflict, "开发版本 Panel 不提供 Agent 升级脚本")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.ReplaceAll(upgradeAgentScript, "__PANEL_VERSION__", version)))
}
