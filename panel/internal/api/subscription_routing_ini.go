package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type routingINIPreviewRequest struct {
	Source                 string `json:"source"`
	PresetID               *int64 `json:"preset_id,omitempty"`
	PersonalSubscriptionID *int64 `json:"personal_subscription_id,omitempty"`
	SubscriberUserID       *int64 `json:"subscriber_user_id,omitempty"`
}
type routingINICreateRequest struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Source  string `json:"source"`
}
type routingINIUpdateRequest struct {
	Revision string `json:"revision"`
	Source   string `json:"source"`
}

type routingINIPreviewResponse struct {
	subscriptionstore.RoutingINIPreview
	Rendering map[string]string `json:"rendering,omitempty"`
}

func (s *server) routingINIPreviewForUser(ctx context.Context, user auth.User, source string, presetID, personalID, subscriberID *int64) (routingINIPreviewResponse, error) {
	preview, err := s.subscriptions.PreviewRoutingINI(ctx, source, presetID)
	out := routingINIPreviewResponse{RoutingINIPreview: preview}
	if err != nil || (personalID == nil && subscriberID == nil) || len(preview.Result.Diagnostics) > 0 {
		return out, err
	}
	if personalID != nil && subscriberID != nil {
		return out, errors.New("choose only one subscription context")
	}
	if subscriberID != nil {
		if user.Role != auth.RoleAdmin {
			return out, subscriptionstore.ErrSubscriberNotFound
		}
		if err := s.mcpVisibleSubscriber(ctx, mcpIdentity{user: user}, *subscriberID); err != nil {
			return out, subscriptionstore.ErrSubscriberNotFound
		}
		data, err := s.subscriptions.PreviewSubscriptionDataForUser(ctx, *subscriberID)
		if errors.Is(err, subscriptionstore.ErrSubscriptionUnavailable) {
			out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "共享订阅没有已协调的可用节点，无法渲染"})
			out.Rendering = map[string]string{"mihomo": "unavailable", "shadowrocket": "unavailable"}
			return out, nil
		}
		if err != nil {
			return out, err
		}
		names := make([]string, 0, len(data.Nodes))
		for _, node := range data.Nodes {
			names = append(names, node.DisplayName)
		}
		addRoutingINIMatchWarnings(&out, names)
		if presetID == nil || data.RoutingPreset == nil || data.RoutingPreset.ID != *presetID {
			data.RoutingBindings = nil
		}
		data.RoutingPreset = &subscriptionstore.RoutingPreset{Name: "INI preview", Enabled: true, Groups: out.Result.Groups, RuleProviders: out.Result.RuleProviders, Rules: out.Result.Rules}
		out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "共享订阅预览使用当前保存的客户端状态，不执行协调；后续状态可能变化"})
		out.Rendering = map[string]string{"mihomo": "success", "shadowrocket": "success"}
		if _, err := subscriptionstore.RenderMihomoSubscription(data); err != nil {
			out.Rendering["mihomo"] = "failed"
			out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "Mihomo 未能渲染当前订阅，请检查节点和策略组成员"})
		}
		if _, err := subscriptionstore.RenderShadowrocketSubscription(data); err != nil {
			out.Rendering["shadowrocket"] = "failed"
			out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "Shadowrocket 未能渲染当前订阅，请检查客户端兼容性"})
		}
		return out, nil
	}
	group, err := s.subscriptions.GetPersonalSubscription(ctx, personalActor(user), *personalID)
	if err != nil {
		return out, err
	}
	data, err := s.subscriptions.GeneratePersonalSubscriptionDataForOwner(ctx, personalActor(user), *personalID)
	if errors.Is(err, subscriptionstore.ErrPersonalSubscriptionEmpty) {
		out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "所选订阅没有可用节点，无法渲染"})
		out.Rendering = map[string]string{"mihomo": "unavailable", "shadowrocket": "unavailable"}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	names := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		names = append(names, node.Name)
	}
	addRoutingINIMatchWarnings(&out, names)
	if presetID == nil || group.RoutingPresetID != *presetID {
		data.RoutingBindings = nil
	}
	data.RoutingPreset = &subscriptionstore.RoutingPreset{Name: "INI preview", Enabled: true, Groups: out.Result.Groups, RuleProviders: out.Result.RuleProviders, Rules: out.Result.Rules}
	out.Rendering = map[string]string{"mihomo": "success", "shadowrocket": "success"}
	if _, err := subscriptionstore.RenderPersonalMihomoSubscription(data); err != nil {
		out.Rendering["mihomo"] = "failed"
		out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "Mihomo 未能渲染当前订阅，请检查节点和策略组成员"})
	}
	if _, err := subscriptionstore.RenderPersonalShadowrocketSubscription(data); err != nil {
		out.Rendering["shadowrocket"] = "failed"
		out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "preview", Message: "Shadowrocket 未能渲染当前订阅，请检查客户端兼容性"})
	}
	return out, nil
}

func addRoutingINIMatchWarnings(out *routingINIPreviewResponse, names []string) {
	warnings := out.Result.Warnings[:0]
	for _, warning := range out.Result.Warnings {
		if warning.Suggestion != "保存后检查实际订阅预览" {
			warnings = append(warnings, warning)
		}
	}
	out.Result.Warnings = warnings
	for _, g := range out.Result.Groups {
		if len(g.NodePatterns) == 0 {
			continue
		}
		patterns := make([]*regexp.Regexp, 0, len(g.NodePatterns))
		for _, pattern := range g.NodePatterns {
			if re, compileErr := regexp.Compile(pattern); compileErr == nil {
				patterns = append(patterns, re)
			}
		}
		matches := 0
		for _, name := range names {
			for _, re := range patterns {
				if re.MatchString(name) {
					matches++
					break
				}
			}
		}
		if matches == 0 {
			out.Result.Warnings = append(out.Result.Warnings, subscriptionstore.RoutingINIDiagnostic{Directive: "custom_proxy_group", Message: "策略组 " + g.Name + " 的正则未匹配任何可用节点"})
		}
	}
}

func (s *server) getRoutingINI(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "分流方案 ID 无效")
	if !ok {
		return
	}
	p, err := s.subscriptions.GetRoutingPreset(r.Context(), id)
	if err != nil {
		writeSubscriptionConfigurationError(w, err)
		return
	}
	ini, err := subscriptionstore.ExportRoutingINI(p)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "format": subscriptionstore.RoutingINIFormat, "source": ini, "revision": subscriptionstore.RoutingINIRevision(p)})
}
func (s *server) previewRoutingINI(w http.ResponseWriter, r *http.Request, user auth.User) {
	var in routingINIPreviewRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.routingINIPreviewForUser(r.Context(), user, in.Source, in.PresetID, in.PersonalSubscriptionID, in.SubscriberUserID)
	if err != nil {
		if errors.Is(err, subscriptionstore.ErrPersonalSubscriptionNotFound) || errors.Is(err, subscriptionstore.ErrSubscriberNotFound) {
			writeError(w, http.StatusNotFound, "个人订阅不存在")
		} else {
			writeSubscriptionConfigurationError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (s *server) createRoutingINI(w http.ResponseWriter, r *http.Request, user auth.User) {
	var in routingINICreateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	value, preview, err := s.subscriptions.CreateRoutingFromINI(r.Context(), in.Name, enabled, in.Source)
	if err != nil {
		writeRoutingINIError(w, err)
		return
	}
	s.recordAudit(r, user, "routing_preset.create", "routing_preset", value.ID, "通过 INI 创建分流方案")
	writeJSON(w, http.StatusCreated, map[string]any{"saved": true, "id": value.ID, "revision": subscriptionstore.RoutingINIRevision(value), "preview": preview, "routing_preset": toRoutingPresetResponse(value)})
}
func (s *server) updateRoutingINI(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "分流方案 ID 无效")
	if !ok {
		return
	}
	var in routingINIUpdateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	value, preview, err := s.subscriptions.UpdateRoutingFromINI(r.Context(), id, in.Revision, in.Source)
	if err != nil {
		writeRoutingINIError(w, err)
		return
	}
	s.recordAudit(r, user, "routing_preset.update", "routing_preset", value.ID, "通过 INI 更新分流方案")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "id": value.ID, "revision": subscriptionstore.RoutingINIRevision(value), "preview": preview, "routing_preset": toRoutingPresetResponse(value)})
}
func writeRoutingINIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrRoutingINIInvalid):
		writeError(w, http.StatusBadRequest, "INI 无效，请先验证")
	case errors.Is(err, subscriptionstore.ErrRoutingINIConflict):
		writeError(w, http.StatusConflict, "分流方案已更改，请重新读取并预览")
	case errors.Is(err, subscriptionstore.ErrRoutingINIBindings):
		writeError(w, http.StatusConflict, "删除的策略组仍有关联节点，请先处理绑定")
	default:
		writeSubscriptionConfigurationError(w, err)
	}
}
