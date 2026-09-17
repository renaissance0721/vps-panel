package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

func (s *server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var request agentRegistrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	registered, err := s.agents.RegisterAgent(
		r.Context(), request.EnrollmentToken, request.AgentVersion, request.ExistingConfig,
	)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentRegistrationResponse{
		AgentID:    registered.ID,
		ServerID:   registered.ServerID,
		AgentToken: registered.Token,
	})
}

func (s *server) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	state, err := s.agents.GetDesiredState(r.Context(), agent.ID, agent.ServerID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentDesiredStateResponse{
		Version: state.Version,
		Xray: agentDesiredXrayState{
			Enabled: len(state.Proxies) > 0,
			Proxies: state.Proxies,
		},
		Realm: agentDesiredRealmState{
			Enabled: len(state.Relays) > 0,
			Relays:  state.Relays,
		},
	})
}

func (s *server) recordAgentConfigResult(w http.ResponseWriter, r *http.Request) {
	agent, agentToken, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	var request agentConfigResultRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Message != "" && strings.Contains(request.Message, agentToken) {
		writeError(w, http.StatusBadRequest, "Agent 配置同步结果无效")
		return
	}
	if err := s.agents.RecordConfigResult(r.Context(), agent.ID, agent.ServerID, agentcontrol.ConfigResult{
		Version: request.Version,
		Status:  request.Status,
		Message: request.Message,
	}); err != nil {
		writeServerError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *server) recordAgentUpgradeResult(w http.ResponseWriter, r *http.Request) {
	agent, agentToken, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	var request agentUpgradeResultRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Status != "failed" || strings.Contains(request.Message, agentToken) {
		writeError(w, http.StatusBadRequest, "Agent 升级结果无效")
		return
	}
	if err := s.agents.RecordAgentUpgradeFailure(
		r.Context(), agent.ID, agent.ServerID, request.Version, request.Message,
	); err != nil {
		writeServerError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *server) authenticateAgentRequest(w http.ResponseWriter, r *http.Request) (agentcontrol.Agent, string, bool) {
	authorization := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return agentcontrol.Agent{}, "", false
	}
	agent, err := s.agents.AuthenticateAgent(r.Context(), authorization[1])
	if errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return agentcontrol.Agent{}, "", false
	}
	if err != nil {
		writeInternalError(w)
		return agentcontrol.Agent{}, "", false
	}
	return agent, authorization[1], true
}

func (s *server) createEnrollment(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	created, err := s.servers.CreateEnrollment(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.agents.CloseConnections(id)
	writeJSON(w, http.StatusCreated, s.toCreatedServerResponse(created, requestBaseURL(r)))
}

func (s *server) upgradeAgent(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, id) {
		return
	}
	targetVersion := formalReleaseVersion(s.panelVersion)
	if targetVersion == "" {
		writeError(w, http.StatusConflict, "开发版本 Panel 不支持一键升级 Agent")
		return
	}
	upgrade, err := s.agents.PrepareAgentUpgrade(r.Context(), id, targetVersion)
	if err != nil {
		writeServerError(w, err)
		return
	}
	if upgrade.AlreadyCurrent {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "already_current", "version": targetVersion,
		})
		return
	}
	if err := s.agents.NotifyAgentUpgrade(id, targetVersion); err != nil {
		if errors.Is(err, agentcontrol.ErrAgentAlreadyCurrent) {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "already_current", "version": targetVersion,
			})
			return
		}
		failureContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.agents.MarkAgentUpgradeFailed(failureContext, id, targetVersion, "无法向在线 Agent 发送升级指令")
		cancel()
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "upgrading", "version": targetVersion,
	})
}

func releaseVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'v' || value[1] < '0' || value[1] > '9' {
		return ""
	}
	for _, character := range value[2:] {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '-' || character == '_' {
			continue
		}
		return ""
	}
	return value
}

func formalReleaseVersion(value string) string {
	if agentcontrol.IsFormalReleaseVersion(value) {
		return value
	}
	return ""
}
