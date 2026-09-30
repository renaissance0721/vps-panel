package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type routingPresetRequest struct {
	Name    string                           `json:"name"`
	Enabled *bool                            `json:"enabled"`
	Groups  []subscriptionstore.RoutingGroup `json:"groups"`
	Rules   []subscriptionstore.RoutingRule  `json:"rules"`
}

type updateRoutingPresetRequest struct {
	Name    *string                           `json:"name"`
	Enabled *bool                             `json:"enabled"`
	Groups  *[]subscriptionstore.RoutingGroup `json:"groups"`
	Rules   *[]subscriptionstore.RoutingRule  `json:"rules"`
}

type routingPresetResponse struct {
	ID        int64                            `json:"id"`
	Name      string                           `json:"name"`
	Enabled   bool                             `json:"enabled"`
	Groups    []subscriptionstore.RoutingGroup `json:"groups"`
	Rules     []subscriptionstore.RoutingRule  `json:"rules"`
	CreatedAt time.Time                        `json:"created_at"`
	UpdatedAt time.Time                        `json:"updated_at"`
}

type subscriptionTemplateRequest struct {
	Name       string `json:"name"`
	Enabled    *bool  `json:"enabled"`
	ConfigYAML string `json:"config_yaml"`
}

type updateSubscriptionTemplateRequest struct {
	Name       *string `json:"name"`
	Enabled    *bool   `json:"enabled"`
	ConfigYAML *string `json:"config_yaml"`
}

type subscriptionTemplateResponse struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	ConfigYAML string    `json:"config_yaml"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *server) listSubscriptionRoutingPresets(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListRoutingPresets(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]routingPresetResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toRoutingPresetResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"routing_presets": response})
}

func (s *server) createSubscriptionRoutingPreset(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request routingPresetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, err := s.subscriptions.CreateRoutingPreset(r.Context(), subscriptionstore.CreateRoutingPresetInput{
		Name: request.Name, Enabled: enabled, Groups: request.Groups, Rules: request.Rules,
	})
	if err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "routing_preset.create", "routing_preset", value.ID, "创建分流预设 "+value.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"routing_preset": toRoutingPresetResponse(value)})
}

func (s *server) updateSubscriptionRoutingPreset(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "分流预设 ID 无效")
	if !ok {
		return
	}
	var request updateRoutingPresetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := s.subscriptions.UpdateRoutingPreset(r.Context(), id, subscriptionstore.UpdateRoutingPresetInput{
		Name: request.Name, Enabled: request.Enabled, Groups: request.Groups, Rules: request.Rules,
	})
	if err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "routing_preset.update", "routing_preset", value.ID, "更新分流预设 "+value.Name)
	writeJSON(w, http.StatusOK, map[string]any{"routing_preset": toRoutingPresetResponse(value)})
}

func (s *server) deleteSubscriptionRoutingPreset(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "分流预设 ID 无效")
	if !ok {
		return
	}
	if err := s.subscriptions.DeleteRoutingPreset(r.Context(), id); err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "routing_preset.delete", "routing_preset", id, "删除分流预设")
	writeNoContent(w)
}

func (s *server) listSubscriptionTemplates(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListTemplates(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]subscriptionTemplateResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toSubscriptionTemplateResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": response})
}

func (s *server) createSubscriptionTemplate(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request subscriptionTemplateRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, err := s.subscriptions.CreateTemplate(r.Context(), subscriptionstore.CreateSubscriptionTemplateInput{
		Name: request.Name, Enabled: enabled, ConfigYAML: request.ConfigYAML,
	})
	if err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "template.create", "subscription_template", value.ID, "创建订阅模板 "+value.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"template": toSubscriptionTemplateResponse(value)})
}

func (s *server) updateSubscriptionTemplate(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅模板 ID 无效")
	if !ok {
		return
	}
	var request updateSubscriptionTemplateRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := s.subscriptions.UpdateTemplate(r.Context(), id, subscriptionstore.UpdateSubscriptionTemplateInput{
		Name: request.Name, Enabled: request.Enabled, ConfigYAML: request.ConfigYAML,
	})
	if err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "template.update", "subscription_template", value.ID, "更新订阅模板 "+value.Name)
	writeJSON(w, http.StatusOK, map[string]any{"template": toSubscriptionTemplateResponse(value)})
}

func (s *server) deleteSubscriptionTemplate(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅模板 ID 无效")
	if !ok {
		return
	}
	if err := s.subscriptions.DeleteTemplate(r.Context(), id); err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	s.recordAudit(r, user, "template.delete", "subscription_template", id, "删除订阅模板")
	writeNoContent(w)
}

func toRoutingPresetResponse(value subscriptionstore.RoutingPreset) routingPresetResponse {
	return routingPresetResponse{ID: value.ID, Name: value.Name, Enabled: value.Enabled,
		Groups: value.Groups, Rules: value.Rules, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func toSubscriptionTemplateResponse(value subscriptionstore.SubscriptionTemplate) subscriptionTemplateResponse {
	return subscriptionTemplateResponse{ID: value.ID, Name: value.Name, Enabled: value.Enabled,
		ConfigYAML: value.ConfigYAML, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func writeSubscriptionConfigurationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrRoutingPresetNotFound):
		writeError(w, http.StatusNotFound, "分流预设不存在")
	case errors.Is(err, subscriptionstore.ErrTemplateNotFound):
		writeError(w, http.StatusNotFound, "订阅模板不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidRoutingPreset), errors.Is(err, subscriptionstore.ErrPublishedNodeNotFound):
		writeError(w, http.StatusBadRequest, "分流预设无效，请检查分组、节点引用和规则")
	case errors.Is(err, subscriptionstore.ErrInvalidTemplate):
		writeError(w, http.StatusBadRequest, "订阅模板不是安全有效的 YAML，且不能覆盖 proxies、proxy-groups 或 rules")
	case errors.Is(err, subscriptionstore.ErrRoutingPresetReferenced):
		writeError(w, http.StatusConflict, "请先解除套餐对该分流预设的引用")
	case errors.Is(err, subscriptionstore.ErrTemplateReferenced):
		writeError(w, http.StatusConflict, "请先解除套餐对该订阅模板的引用")
	default:
		writeInternalError(w, err)
	}
}
