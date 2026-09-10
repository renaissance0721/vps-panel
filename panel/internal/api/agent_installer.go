package api

import (
	_ "embed"
	"net/http"
)

//go:embed install-agent.sh
var installAgentScript []byte

func (s *server) installAgent(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(installAgentScript)
}
