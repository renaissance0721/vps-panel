package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type createRelayRequest struct {
	ServerID      int64  `json:"server_id"`
	Name          string `json:"name"`
	ListenAddress string `json:"listen_address"`
	ListenPort    int    `json:"listen_port"`
	TargetType    string `json:"target_type"`
	TargetProxyID *int64 `json:"target_proxy_id"`
	TargetHost    string `json:"target_host"`
	TargetPort    int    `json:"target_port"`
	Network       string `json:"network"`
	Enabled       *bool  `json:"enabled"`
}

type updateRelayRequest struct {
	Name          *string `json:"name"`
	ListenAddress *string `json:"listen_address"`
	ListenPort    *int    `json:"listen_port"`
	TargetType    *string `json:"target_type"`
	TargetProxyID *int64  `json:"target_proxy_id"`
	TargetHost    *string `json:"target_host"`
	TargetPort    *int    `json:"target_port"`
	Network       *string `json:"network"`
	Enabled       *bool   `json:"enabled"`
}

type relayResponse struct {
	ID                 int64     `json:"id"`
	ServerID           int64     `json:"server_id"`
	ServerName         string    `json:"server_name"`
	ServerPublicIPv4   string    `json:"server_public_ipv4"`
	Name               string    `json:"name"`
	ListenAddress      string    `json:"listen_address"`
	ListenPort         int       `json:"listen_port"`
	TargetType         string    `json:"target_type"`
	TargetProxyID      *int64    `json:"target_proxy_id"`
	TargetProxyName    string    `json:"target_proxy_name"`
	TargetHost         string    `json:"target_host"`
	TargetPort         int       `json:"target_port"`
	TargetAddressReady bool      `json:"target_address_ready"`
	Network            string    `json:"network"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (s *server) listRelays(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.relays.List(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]relayResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toRelayResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": response})
}

func (s *server) createRelay(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var request createRelayRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, mutation, err := s.relays.Create(r.Context(), relaystore.CreateInput{
		ServerID: request.ServerID, Name: request.Name, ListenAddress: request.ListenAddress,
		ListenPort: request.ListenPort, TargetType: request.TargetType,
		TargetProxyID: request.TargetProxyID, TargetHost: request.TargetHost,
		TargetPort: request.TargetPort, Network: request.Network, Enabled: enabled,
	})
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeJSON(w, http.StatusCreated, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) getRelay(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	value, err := s.relays.Get(r.Context(), id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) updateRelay(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	var request updateRelayRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutation, err := s.relays.Update(r.Context(), id, relaystore.UpdateInput{
		Name: request.Name, ListenAddress: request.ListenAddress, ListenPort: request.ListenPort,
		TargetType: request.TargetType, TargetProxyID: request.TargetProxyID,
		TargetHost: request.TargetHost, TargetPort: request.TargetPort,
		Network: request.Network, Enabled: request.Enabled,
	})
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeJSON(w, http.StatusOK, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) deleteRelay(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	mutation, err := s.relays.Delete(r.Context(), id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeNoContent(w)
}

func (s *server) notifyRelayMutations(mutations []relaystore.Mutation) {
	for _, mutation := range mutations {
		if err := s.notifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
			log.Printf("notify Agent for server %d config version %d: %v", mutation.ServerID, mutation.Version, err)
		}
	}
}

func toRelayResponse(value relaystore.Relay) relayResponse {
	return relayResponse{
		ID: value.ID, ServerID: value.ServerID, ServerName: value.ServerName,
		ServerPublicIPv4: value.ServerPublicIPv4, Name: value.Name,
		ListenAddress: value.ListenAddress, ListenPort: value.ListenPort,
		TargetType: value.TargetType, TargetProxyID: value.TargetProxyID,
		TargetProxyName: value.TargetProxyName, TargetHost: value.TargetHost,
		TargetPort: value.TargetPort, TargetAddressReady: value.TargetAddressReady,
		Network: value.Network, Enabled: value.Enabled,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func writeRelayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, relaystore.ErrNotFound):
		writeError(w, http.StatusNotFound, "中转规则不存在")
	case errors.Is(err, relaystore.ErrServerNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在或已移除")
	case errors.Is(err, relaystore.ErrProxyNotFound):
		writeError(w, http.StatusNotFound, "目标代理节点不存在或已移除")
	case errors.Is(err, relaystore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "名称不能为空且不能超过 100 个字符")
	case errors.Is(err, relaystore.ErrInvalidListenIP):
		writeError(w, http.StatusBadRequest, "监听地址必须是有效 IP")
	case errors.Is(err, relaystore.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "端口必须在 1–65535 之间")
	case errors.Is(err, relaystore.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, "目标必须是有效代理节点或 Host/IP 与端口")
	case errors.Is(err, relaystore.ErrInvalidNetwork):
		writeError(w, http.StatusBadRequest, "Network 仅支持 TCP、UDP 或 TCP + UDP")
	case errors.Is(err, relaystore.ErrPortConflict):
		writeError(w, http.StatusConflict, "该服务器上的监听端口与现有代理节点或中转规则冲突")
	case errors.Is(err, relaystore.ErrTargetUnavailable):
		writeError(w, http.StatusConflict, "目标代理节点入口地址不可用，请填写手动入口地址或等待目标服务器上报公网 IPv4")
	default:
		writeInternalError(w)
	}
}
