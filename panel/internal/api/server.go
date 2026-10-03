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
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
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
		writeInternalError(w, err)
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
		writeInternalError(w, err)
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
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	expiresAt, err := parseServerExpiration(request.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
		return
	}
	renewalPeriod, err := parseOptionalInt(request.RenewalPeriodMonths)
	if err != nil {
		writeError(w, http.StatusBadRequest, "续费周期无效")
		return
	}
	monthlyLimit, err := parseOptionalInt64(request.MonthlyTrafficLimitBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "月流量额度格式无效")
		return
	}
	traffic := serverstore.DefaultTrafficConfig()
	if request.TrafficCountMode != nil {
		if *request.TrafficCountMode == "" {
			writeServerError(w, serverstore.ErrInvalidTrafficConfig)
			return
		}
		traffic.CountMode = *request.TrafficCountMode
	}
	if request.TrafficResetDay != nil {
		if *request.TrafficResetDay < 1 || *request.TrafficResetDay > 31 {
			writeServerError(w, serverstore.ErrInvalidTrafficConfig)
			return
		}
		traffic.ResetDay = *request.TrafficResetDay
	}
	if request.TrafficResetTime != nil {
		if *request.TrafficResetTime == "" {
			writeServerError(w, serverstore.ErrInvalidTrafficConfig)
			return
		}
		traffic.ResetTime = *request.TrafficResetTime
	}
	// Keep inheritance ordered with probe PATCH's read/replace of assignments.
	s.probeMu.Lock()
	created, err := s.servers.CreateWithSettings(r.Context(), serverstore.CreateServerInput{
		Name: request.Name, BoundDomainIPv4: request.BoundDomainIPv4, BoundDomainIPv6: request.BoundDomainIPv6,
		Visibility: request.Visibility, UserIDs: request.UserIDs, CreatorID: user.ID,
		ExpiresAt: expiresAt, RenewalPeriodMonths: renewalPeriod, AutoRenew: request.AutoRenew,
		MonthlyTrafficLimitBytes: monthlyLimit, TrafficCountMode: traffic.CountMode,
		TrafficResetDay: traffic.ResetDay, TrafficResetTime: traffic.ResetTime,
	})
	s.probeMu.Unlock()
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.recordAudit(r, user, "server.create", "server", created.ID, "创建服务器 "+created.Name)
	writeJSON(w, http.StatusCreated, s.toCreatedServerResponse(created, baseURL))
}

func parseServerExpiration(raw json.RawMessage) (*time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid server expiration")
	}
	parsed, err := time.ParseInLocation(expirationDateLayout, value, shanghaiLocation)
	if err != nil {
		return nil, err
	}
	parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, shanghaiLocation).UTC()
	return &parsed, nil
}

func parseOptionalInt(raw json.RawMessage) (*int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid integer")
	}
	return &value, nil
}

func parseOptionalInt64(raw json.RawMessage) (*int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid integer")
	}
	return &value, nil
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
	if !s.requireMutableServer(w, r, id) {
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
	s.recordAudit(r, user, "server.update", "server", id, "更新服务器访问权限")
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
	if !s.requireMutableServer(w, r, id) {
		return
	}
	var request updateServerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	hasExpiration := len(request.ExpiresAt) != 0
	hasRenewalPeriod := len(request.RenewalPeriodMonths) != 0
	hasAutoRenew := request.AutoRenew != nil
	hasRenewalSettings := hasExpiration || hasRenewalPeriod || hasAutoRenew
	hasName := request.Name != nil
	hasBoundDomain := request.BoundDomainIPv4 != nil || request.BoundDomainIPv6 != nil
	hasOwner := len(request.OwnerUserID) != 0
	hasOutboundPreference := request.OutboundPreference != nil
	hasBlockChinaInbound := request.BlockChinaInbound != nil
	hasAnyTraffic := len(request.MonthlyTrafficLimitBytes) != 0 || request.TrafficCountMode != nil ||
		request.TrafficResetDay != nil || request.TrafficResetTime != nil
	settingCount := 0
	for _, present := range []bool{hasName, hasBoundDomain, hasOwner, hasRenewalSettings, hasAnyTraffic, hasOutboundPreference, hasBlockChinaInbound} {
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
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器名称")
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasBoundDomain {
		updated, err := s.servers.UpdateBoundDomains(r.Context(), id, request.BoundDomainIPv4, request.BoundDomainIPv6)
		if err != nil {
			writeServerError(w, err)
			return
		}
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器绑定域名")
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasOwner {
		var ownerUserID *int64
		if json.Unmarshal(request.OwnerUserID, &ownerUserID) != nil {
			writeServerError(w, serverstore.ErrInvalidServerOwner)
			return
		}
		updated, err := s.servers.UpdateOwner(r.Context(), id, ownerUserID)
		if err != nil {
			writeServerError(w, err)
			return
		}
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器所有者")
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasOutboundPreference {
		preference := *request.OutboundPreference
		if preference != serverstore.OutboundAuto && preference != serverstore.OutboundPreferIPv4 && preference != serverstore.OutboundPreferIPv6 {
			writeServerError(w, serverstore.ErrInvalidOutboundPreference)
			return
		}
		if preference != serverstore.OutboundAuto {
			supported, err := s.serverSupportsCapability(r, id, agentcontrol.CapabilityOutboundPreference)
			if err != nil {
				writeServerError(w, err)
				return
			}
			if !supported {
				writeError(w, http.StatusConflict, "当前 Agent 不支持出站 IPv4 / IPv6 偏好")
				return
			}
		}
		updated, version, err := s.servers.UpdateOutboundPreference(r.Context(), id, *request.OutboundPreference)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if err := s.agents.NotifyConfigChanged(id, version); err != nil {
			log.Printf("notify Agent of outbound preference change: %v", err)
		}
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器出站偏好")
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	if hasBlockChinaInbound {
		current, err := s.servers.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if *request.BlockChinaInbound && !current.BlockChinaInbound && !agentcontrol.DeclaresCapability(agentcontrol.Metadata{
			Implementation: current.AgentImplementation,
			Version:        current.AgentVersion,
			APIVersion:     current.AgentAPIVersion,
			Capabilities:   current.AgentCapabilities,
		}, agentcontrol.CapabilityFirewallCNBlock) {
			writeError(w, http.StatusConflict, "当前 Agent 不支持中国 IP 入站限制")
			return
		}
		updated, version, changed, err := s.servers.UpdateBlockChinaInbound(r.Context(), id, *request.BlockChinaInbound)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if changed {
			if err := s.agents.NotifyConfigChanged(id, version); err != nil {
				log.Printf("notify Agent of China inbound block change: %v", err)
			}
		}
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器中国入站限制")
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
		s.recordAudit(r, user, "server.update", "server", id, "更新服务器流量设置")
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
		return
	}
	update := serverstore.RenewalSettingsUpdate{
		ExpiresAtSet:     hasExpiration,
		RenewalPeriodSet: hasRenewalPeriod,
		AutoRenewSet:     hasAutoRenew,
	}
	if hasAutoRenew {
		update.AutoRenew = *request.AutoRenew
	}
	if hasExpiration && string(request.ExpiresAt) != "null" {
		parsed, err := parseServerExpiration(request.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
			return
		}
		update.ExpiresAt = parsed
	}
	if hasRenewalPeriod && string(request.RenewalPeriodMonths) != "null" {
		value, err := parseOptionalInt(request.RenewalPeriodMonths)
		if err != nil {
			writeError(w, http.StatusBadRequest, "续费周期无效")
			return
		}
		update.RenewalPeriodMonths = value
	}
	updated, err := s.servers.UpdateRenewalSettings(r.Context(), id, update)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.recordAudit(r, user, "server.update", "server", id, "更新服务器续费设置")
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated, s.panelVersion)})
}

func (s *server) serverSupportsCapability(r *http.Request, serverID int64, capability string) (bool, error) {
	value, err := s.servers.Get(r.Context(), serverID)
	if err != nil {
		return false, err
	}
	return agentcontrol.SupportsCapability(agentcontrol.Metadata{
		Implementation: value.AgentImplementation,
		Version:        value.AgentVersion,
		APIVersion:     value.AgentAPIVersion,
		Capabilities:   value.AgentCapabilities,
	}, capability), nil
}

func (s *server) updateTrafficAdjustment(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	if !s.requireMutableServer(w, r, id) {
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
	s.recordAudit(r, user, "server.update", "server", id, "校准服务器流量")
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
	if !s.requireMutableServer(w, r, id) {
		return
	}
	updated, err := s.servers.ClearTrafficAdjustment(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.recordAudit(r, user, "server.update", "server", id, "清除服务器流量校准")
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
	version, err := s.servers.RequestDecommission(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	if err := s.agents.NotifyConfigChanged(id, version); err != nil {
		log.Printf("notify Agent of server decommission: %v", err)
	}
	s.recordAudit(r, user, "server.delete", "server", id, "请求下线服务器")
	writeNoContent(w)
}

func (s *server) getServerDependencies(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	summary, err := s.servers.GetDependencySummary(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	relays := make([]map[string]string, 0, len(summary.ReferencingRelays))
	for _, value := range summary.ReferencingRelays {
		relays = append(relays, map[string]string{"name": value.Name, "source_server_name": value.SourceServerName})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy_count": summary.ProxyCount, "client_count": summary.ClientCount,
		"owned_relay_count": summary.OwnedRelayCount, "published_node_count": summary.PublishedNodeCount,
		"personal_node_count": summary.PersonalNodeCount, "subscriber_client_count": summary.SubscriberClientCount,
		"referencing_relays": relays,
	})
}

func (s *server) forceRemoveServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	mutations, err := s.servers.ForceArchiveWithMutations(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.agents.CloseConnections(id)
	s.notifyServerMutations(mutations)
	s.recordAudit(r, user, "server.archive", "server", id, "强制归档服务器")
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
	mutations, err := s.servers.PermanentlyDeleteWithMutations(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.agents.CloseConnections(id)
	s.notifyServerMutations(mutations)
	s.recordAudit(r, user, "server.delete", "server", id, "永久删除服务器")
	writeNoContent(w)
}

func (s *server) notifyServerMutations(mutations []serverstore.ConfigMutation) {
	for _, mutation := range mutations {
		if err := s.agents.NotifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
			log.Printf("notify Agent for server %d dependency cleanup version %d: %v", mutation.ServerID, mutation.Version, err)
		}
	}
}

func writeServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, monitor.ErrTaskLimit):
		writeMonitorError(w, err)
	case errors.Is(err, serverstore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "服务器名称不能为空且不能超过 100 个字符")
	case errors.Is(err, serverstore.ErrInvalidBoundDomain):
		writeError(w, http.StatusBadRequest, "服务器绑定域名格式无效")
	case errors.Is(err, serverstore.ErrIPv6Unavailable):
		writeError(w, http.StatusConflict, "当前服务器未检测到 IPv6 地址")
	case errors.Is(err, serverstore.ErrInvalidVisibility):
		writeError(w, http.StatusBadRequest, "服务器可见范围无效")
	case errors.Is(err, serverstore.ErrInvalidServerAccess):
		writeError(w, http.StatusBadRequest, "服务器访问账号无效")
	case errors.Is(err, serverstore.ErrInvalidServerOwner):
		writeError(w, http.StatusBadRequest, "服务器所有者账号无效")
	case errors.Is(err, serverstore.ErrInvalidOutboundPreference):
		writeError(w, http.StatusBadRequest, "服务器出站优先级无效")
	case errors.Is(err, serverstore.ErrInvalidRenewalPeriod):
		writeError(w, http.StatusBadRequest, "续费周期无效")
	case errors.Is(err, serverstore.ErrAutoRenewRequirements):
		writeError(w, http.StatusBadRequest, "自动续费需要设置到期日期和续费周期")
	case errors.Is(err, serverstore.ErrDecommissioning):
		writeError(w, http.StatusConflict, "服务器正在退役，不能继续修改配置")
	case errors.Is(err, serverstore.ErrAgentNotRegistered):
		writeError(w, http.StatusConflict, "服务器尚未注册 Agent，无法自动清理；管理员可以强制从 Panel 移除")
	case errors.Is(err, serverstore.ErrDecommissionUnsupported):
		writeError(w, http.StatusConflict, "当前 Agent 不支持自动清理，请先升级 Agent，或由管理员强制从 Panel 移除")
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
		writeError(w, http.StatusConflict, "中转目标地址不可用，请设置目标 Proxy 的手动入口地址或等待目标服务器上报对应公网地址")
	default:
		writeInternalError(w, err)
	}
}
