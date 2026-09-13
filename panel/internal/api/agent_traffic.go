package api

import (
	"errors"
	"net/http"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type agentClientTrafficRequest struct {
	Clients []agentClientTrafficItem `json:"clients"`
}

type agentClientTrafficItem struct {
	ClientID      int64 `json:"client_id"`
	UplinkBytes   int64 `json:"uplink_bytes"`
	DownlinkBytes int64 `json:"downlink_bytes"`
}

func (s *server) recordAgentClientTraffic(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	var request agentClientTrafficRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	reports := make([]proxystore.ClientTrafficReport, 0, len(request.Clients))
	for _, item := range request.Clients {
		reports = append(reports, proxystore.ClientTrafficReport{
			ClientID: item.ClientID, UplinkBytes: item.UplinkBytes, DownlinkBytes: item.DownlinkBytes,
		})
	}
	mutation, err := s.proxies.RecordClientTrafficWithMutation(r.Context(), agent.ServerID, reports)
	if err != nil {
		if errors.Is(err, proxystore.ErrInvalidClientTraffic) {
			writeError(w, http.StatusBadRequest, "客户端流量数据无效")
			return
		}
		writeInternalError(w)
		return
	}
	if mutation.Version > 0 {
		s.notifyProxyMutation(mutation)
	}
	writeNoContent(w)
}
