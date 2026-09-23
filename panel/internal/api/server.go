package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func (s *server) listServers(w http.ResponseWriter, r *http.Request, user auth.User) {
	var values []serverstore.Server
	var err error
	if r.URL.Query().Get("archived") == "true" {
		values, err = s.servers.ListArchivedForUser(r.Context(), user.ID)
	} else {
		values, err = s.servers.ListForUser(r.Context(), user.ID)
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]serverResponse, 0, len(values))
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		response = append(response, toServerResponse(value, s.panelVersion))
		ids = append(ids, value.ID)
	}
	ranks, err := s.orderRanks(r.Context(), user.ID, listorder.Servers, ids)
	if err != nil {
		writeInternalError(w)
		return
	}
	sort.SliceStable(response, func(i, j int) bool { return ranks[response[i].ID] < ranks[response[j].ID] })
	writeJSON(w, http.StatusOK, map[string]any{"servers": response})
}

func (s *server) createServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createServerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	created, err := s.servers.CreateForUser(r.Context(), request.Name, request.Visibility, request.UserIDs, user.ID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toCreatedServerResponse(created, requestBaseURL(r)))
}

func (s *server) getServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	value, err := s.servers.Get(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(value, s.panelVersion)})
}

func (s *server) updateServerAccess(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	var request updateServerAccessRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	access, err := s.servers.UpdateAccess(r.Context(), id, user.ID, request.Visibility, request.UserIDs)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access": map[string]any{"visibility": access.Visibility, "user_ids": access.UserIDs},
	})
}

func (s *server) updateServerExpiration(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	var request updateServerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	hasExpiration := len(request.ExpiresAt) != 0
	hasName := request.Name != nil
	hasOutboundPreference := request.OutboundPreference != nil
	hasAnyTraffic := len(request.MonthlyTrafficLimitBytes) != 0 || request.TrafficCountMode != nil ||
		request.TrafficResetDay != nil || request.TrafficResetTime != nil
	settingCount := 0
	for _, present := range []bool{hasName, hasExpiration, hasAnyTraffic, hasOutboundPreference} {
		if present {
			settingCount++
		}
	}
	if settingCount != 1 {
		writeError(w, http.StatusBadRequest, "服务器设置格式无效")
		return
	}
	if hasName {
		updated, err := s.servers.UpdateName(r.Context(), id, *request.Name)
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasOutboundPreference {
		updated, version, err := s.servers.UpdateOutboundPreference(r.Context(), id, *request.OutboundPreference)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if err := s.agents.NotifyConfigChanged(id, version); err != nil {
			log.Printf("notify Agent of outbound preference change: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasAnyTraffic {
		if len(request.MonthlyTrafficLimitBytes) == 0 || request.TrafficCountMode == nil ||
			request.TrafficResetDay == nil || request.TrafficResetTime == nil {
			writeError(w, http.StatusBadRequest, "月流量设置不完整")
			return
		}
		var monthlyLimit *int64
		if string(request.MonthlyTrafficLimitBytes) != "null" {
			var value int64
			if json.Unmarshal(request.MonthlyTrafficLimitBytes, &value) != nil {
				writeError(w, http.StatusBadRequest, "月流量额度格式无效")
				return
			}
			monthlyLimit = &value
		}
		updated, err := s.servers.UpdateTrafficConfig(r.Context(), id, serverstore.TrafficConfig{
			MonthlyLimitBytes: monthlyLimit,
			CountMode:         *request.TrafficCountMode,
			ResetDay:          *request.TrafficResetDay,
			ResetTime:         *request.TrafficResetTime,
		})
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	var expiresAt *time.Time
	if string(request.ExpiresAt) != "null" {
		var value string
		if json.Unmarshal(request.ExpiresAt, &value) != nil {
			writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
			return
		}
		parsed, err := time.ParseInLocation(expirationDateLayout, value, shanghaiLocation)
		if err != nil {
			writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
			return
		}
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, shanghaiLocation).UTC()
		expiresAt = &parsed
	}
	updated, err := s.servers.UpdateExpiration(r.Context(), id, expiresAt)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
}

func (s *server) updateTrafficAdjustment(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	var request updateTrafficAdjustmentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.TargetUsedBytes == nil {
		writeError(w, http.StatusBadRequest, "目标已用流量格式无效")
		return
	}
	updated, err := s.servers.UpdateTrafficAdjustment(r.Context(), id, *request.TargetUsedBytes)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
}

func (s *server) clearTrafficAdjustment(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	updated, err := s.servers.ClearTrafficAdjustment(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
}

func (s *server) deleteServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	if err := s.servers.Archive(r.Context(), id); err != nil {
		writeServerError(w, err)
		return
	}
	s.agents.CloseConnections(id)
	writeNoContent(w)
}

func (s *server) permanentlyDeleteServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	if err := s.servers.PermanentlyDelete(r.Context(), id); err != nil {
		writeServerError(w, err)
		return
	}
	s.agents.CloseConnections(id)
	writeNoContent(w)
}

func writeServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, serverstore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "服务器名称不能为空且不能超过 100 个字符")
	case errors.Is(err, serverstore.ErrInvalidVisibility):
		writeError(w, http.StatusBadRequest, "服务器可见范围无效")
	case errors.Is(err, serverstore.ErrInvalidServerAccess):
		writeError(w, http.StatusBadRequest, "服务器访问账号无效")
	case errors.Is(err, serverstore.ErrInvalidOutboundPreference):
		writeError(w, http.StatusBadRequest, "服务器出站优先级无效")
	case errors.Is(err, serverstore.ErrNotFound), errors.Is(err, agentcontrol.ErrServerNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在")
	case errors.Is(err, agentcontrol.ErrInvalidEnrollment):
		writeError(w, http.StatusUnauthorized, "Enrollment Token 无效、已使用或已过期")
	case errors.Is(err, agentcontrol.ErrInvalidAgentVersion):
		writeError(w, http.StatusBadRequest, "Agent 版本不能为空且不能超过 64 个字符")
	case errors.Is(err, agentcontrol.ErrUnsupportedAgentAPI):
		writeError(w, http.StatusBadRequest, "不支持该 Agent API 版本")
	case errors.Is(err, agentcontrol.ErrInvalidAgentMetadata):
		writeError(w, http.StatusBadRequest, "Agent 身份元数据无效")
	case errors.Is(err, agentcontrol.ErrAgentImplementationMismatch):
		writeError(w, http.StatusConflict, "Agent implementation 与注册信息不一致")
	case errors.Is(err, serverstore.ErrInvalidTrafficConfig):
		writeError(w, http.StatusBadRequest, "月流量设置无效")
	case errors.Is(err, serverstore.ErrInvalidTrafficTarget):
		writeError(w, http.StatusBadRequest, "目标已用流量必须是非负整数")
	case errors.Is(err, agentcontrol.ErrInvalidConfigResult):
		writeError(w, http.StatusBadRequest, "Agent 配置同步结果无效")
	case errors.Is(err, agentcontrol.ErrConfigVersionAhead):
		writeError(w, http.StatusBadRequest, "Agent 配置版本高于当前目标版本")
	case errors.Is(err, agentcontrol.ErrInvalidAgentToken):
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
	case errors.Is(err, agentcontrol.ErrAgentOffline):
		writeError(w, http.StatusConflict, "Agent 当前不在线")
	case errors.Is(err, agentcontrol.ErrAgentNotRegistered):
		writeError(w, http.StatusConflict, "服务器尚未注册 Agent")
	case errors.Is(err, agentcontrol.ErrAgentNewer):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agentcontrol.ErrUnknownAgentVersion):
		writeError(w, http.StatusConflict, "Agent 版本未知或为开发版本，不能一键升级")
	case errors.Is(err, agentcontrol.ErrInvalidUpgrade):
		writeError(w, http.StatusBadRequest, "Agent 升级请求无效")
	case errors.Is(err, agentcontrol.ErrAgentUpgradeUnsupported):
		writeError(w, http.StatusConflict, "该 Agent 不支持官方自动升级")
	case errors.Is(err, relaystore.ErrTargetUnavailable):
		writeError(w, http.StatusConflict, "中转目标地址不可用，请设置目标 Proxy 的手动入口地址或等待目标服务器上报公网 IPv4")
	default:
		writeInternalError(w)
	}
}
