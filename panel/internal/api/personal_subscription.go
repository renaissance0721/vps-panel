package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type createPersonalSubscriptionRequest struct {
	Name              string `json:"name"`
	SubscriptionTitle string `json:"subscription_title"`
	Enabled           *bool  `json:"enabled"`
	ClientName        string `json:"client_name"`
	RoutingPresetID   *int64 `json:"routing_preset_id"`
	MihomoTemplateID  *int64 `json:"mihomo_template_id"`
}

type updatePersonalSubscriptionRequest struct {
	Name              *string         `json:"name"`
	SubscriptionTitle *string         `json:"subscription_title"`
	Enabled           *bool           `json:"enabled"`
	ClientName        *string         `json:"client_name"`
	RoutingPresetID   *int64          `json:"routing_preset_id"`
	MihomoTemplateID  json.RawMessage `json:"mihomo_template_id"`
}

type setPersonalSubscriptionNodesRequest struct {
	Nodes []personalSubscriptionNodeRequest `json:"nodes"`
}

type personalSubscriptionNodeRequest struct {
	SourceType  string `json:"source_type"`
	SourceID    int64  `json:"source_id"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
}

type personalSubscriptionNodeResponse struct {
	ID           int64     `json:"id"`
	SourceType   string    `json:"source_type"`
	SourceID     int64     `json:"source_id"`
	SourceName   string    `json:"source_name"`
	SourceDetail string    `json:"source_detail"`
	DisplayName  string    `json:"display_name"`
	Enabled      bool      `json:"enabled"`
	Position     int       `json:"position"`
	Status       string    `json:"status"`
	StatusDetail string    `json:"status_detail"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type personalSubscriptionResponse struct {
	ID                    int64                              `json:"id"`
	Name                  string                             `json:"name"`
	SubscriptionTitle     string                             `json:"subscription_title"`
	Enabled               bool                               `json:"enabled"`
	ClientName            string                             `json:"client_name"`
	RoutingPresetID       int64                              `json:"routing_preset_id"`
	RoutingPresetName     string                             `json:"routing_preset_name"`
	MihomoTemplateID      *int64                             `json:"mihomo_template_id"`
	MihomoTemplateName    string                             `json:"mihomo_template_name"`
	Nodes                 []personalSubscriptionNodeResponse `json:"nodes"`
	SubscriptionBase64URL string                             `json:"subscription_base64_url"`
	SubscriptionMihomoURL string                             `json:"subscription_mihomo_url"`
	SubscriptionAutoURL   string                             `json:"subscription_auto_url"`
	CreatedAt             time.Time                          `json:"created_at"`
	UpdatedAt             time.Time                          `json:"updated_at"`
}

type personalSubscriptionSourceResponse struct {
	SourceType   string `json:"source_type"`
	SourceID     int64  `json:"source_id"`
	Name         string `json:"name"`
	Detail       string `json:"detail"`
	DefaultName  string `json:"default_name"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
}

func (s *server) listPersonalSubscriptions(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.subscriptions.ListPersonalSubscriptions(r.Context(), personalActor(user))
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	response := make([]personalSubscriptionResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toPersonalSubscriptionResponse(value, baseURL))
	}
	writeJSON(w, http.StatusOK, map[string]any{"personal_subscriptions": response})
}

func (s *server) createPersonalSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createPersonalSubscriptionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, err := s.subscriptions.CreatePersonalSubscription(r.Context(), personalActor(user),
		subscriptionstore.CreatePersonalSubscriptionInput{
			Name: request.Name, SubscriptionTitle: request.SubscriptionTitle, Enabled: enabled,
			ClientName: request.ClientName, RoutingPresetID: request.RoutingPresetID,
			MihomoTemplateID: request.MihomoTemplateID,
		})
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	s.recordAudit(r, user, "personal_subscription.create", "personal_subscription", value.ID, "创建个人订阅 "+value.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"personal_subscription": toPersonalSubscriptionResponse(value, baseURL)})
}

func (s *server) getPersonalSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	value, err := s.subscriptions.GetPersonalSubscription(r.Context(), personalActor(user), id)
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"personal_subscription": toPersonalSubscriptionResponse(value, baseURL)})
}

func (s *server) updatePersonalSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	var request updatePersonalSubscriptionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	templateID, templateSet, err := decodeNullableInt64(request.MihomoTemplateID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Mihomo 模板格式无效")
		return
	}
	value, err := s.subscriptions.UpdatePersonalSubscription(r.Context(), personalActor(user), id,
		subscriptionstore.UpdatePersonalSubscriptionInput{
			Name: request.Name, SubscriptionTitle: request.SubscriptionTitle, Enabled: request.Enabled,
			ClientName: request.ClientName, RoutingPresetID: request.RoutingPresetID,
			MihomoTemplateIDSet: templateSet, MihomoTemplateID: templateID,
		})
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	s.recordAudit(r, user, "personal_subscription.update", "personal_subscription", value.ID, "更新个人订阅 "+value.Name)
	writeJSON(w, http.StatusOK, map[string]any{"personal_subscription": toPersonalSubscriptionResponse(value, baseURL)})
}

func (s *server) deletePersonalSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	if err := s.subscriptions.DeletePersonalSubscription(r.Context(), personalActor(user), id); err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "personal_subscription.delete", "personal_subscription", id, "删除个人订阅")
	writeNoContent(w)
}

func (s *server) setPersonalSubscriptionNodes(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	var request setPersonalSubscriptionNodesRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	inputs := make([]subscriptionstore.SetPersonalSubscriptionNodeInput, 0, len(request.Nodes))
	for _, node := range request.Nodes {
		inputs = append(inputs, subscriptionstore.SetPersonalSubscriptionNodeInput{
			SourceType: node.SourceType, SourceID: node.SourceID, DisplayName: node.DisplayName, Enabled: node.Enabled,
		})
	}
	value, err := s.subscriptions.SetPersonalSubscriptionNodes(r.Context(), personalActor(user), id, inputs)
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"personal_subscription": toPersonalSubscriptionResponse(value, baseURL)})
}

func (s *server) regeneratePersonalSubscriptionToken(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	value, err := s.subscriptions.RegeneratePersonalSubscriptionToken(r.Context(), personalActor(user), id)
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w, errPanelBaseURL)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"personal_subscription": toPersonalSubscriptionResponse(value, baseURL)})
}

func (s *server) previewPersonalSubscriptionMihomo(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "个人订阅 ID 无效")
	if !ok {
		return
	}
	data, err := s.subscriptions.GeneratePersonalSubscriptionDataForOwner(r.Context(), personalActor(user), id)
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	value, err := subscriptionstore.RenderPersonalMihomoSubscription(data)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"yaml": string(value)})
}

func (s *server) listPersonalSubscriptionSources(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.subscriptions.ListPersonalSubscriptionSources(r.Context(), personalActor(user),
		strings.TrimSpace(r.URL.Query().Get("client_name")))
	if err != nil {
		writePersonalSubscriptionError(w, err)
		return
	}
	response := make([]personalSubscriptionSourceResponse, 0, len(values))
	for _, value := range values {
		response = append(response, personalSubscriptionSourceResponse{
			SourceType: value.SourceType, SourceID: value.SourceID, Name: value.Name, Detail: value.Detail,
			DefaultName: value.DefaultName, Status: value.Status, StatusDetail: value.StatusDetail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": response})
}

func personalActor(user auth.User) subscriptionstore.PersonalSubscriptionActor {
	return subscriptionstore.PersonalSubscriptionActor{UserID: user.ID, Role: user.Role}
}

func toPersonalSubscriptionResponse(value subscriptionstore.PersonalSubscription, baseURL string) personalSubscriptionResponse {
	nodes := make([]personalSubscriptionNodeResponse, 0, len(value.Nodes))
	for _, node := range value.Nodes {
		nodes = append(nodes, personalSubscriptionNodeResponse{
			ID: node.ID, SourceType: node.SourceType, SourceID: node.SourceID,
			SourceName: node.SourceName, SourceDetail: node.SourceDetail, DisplayName: node.DisplayName,
			Enabled: node.Enabled, Position: node.Position, Status: node.Status, StatusDetail: node.StatusDetail,
			CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt,
		})
	}
	urls := buildPersonalSubscriptionURLs(baseURL, value.Token)
	return personalSubscriptionResponse{
		ID: value.ID, Name: value.Name, SubscriptionTitle: value.SubscriptionTitle, Enabled: value.Enabled,
		ClientName: value.ClientName, RoutingPresetID: value.RoutingPresetID,
		RoutingPresetName: value.RoutingPresetName, MihomoTemplateID: value.MihomoTemplateID,
		MihomoTemplateName: value.MihomoTemplateName, Nodes: nodes,
		SubscriptionBase64URL: urls.Base64, SubscriptionMihomoURL: urls.Mihomo, SubscriptionAutoURL: urls.Auto,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func buildPersonalSubscriptionURLs(baseURL, tokenValue string) subscriptionURLSet {
	base := baseURL + "/sub/personal/" + url.PathEscape(tokenValue)
	return subscriptionURLSet{Base64: base, Mihomo: base + "/mihomo", Auto: base + "/auto"}
}

func writePersonalSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrPersonalSubscriptionNotFound):
		writeError(w, http.StatusNotFound, "个人订阅不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidPersonalSubscription):
		writeError(w, http.StatusBadRequest, "个人订阅名称、订阅标题或 Client 名称无效")
	case errors.Is(err, subscriptionstore.ErrInvalidPersonalNodes):
		writeError(w, http.StatusBadRequest, "个人订阅节点列表无效，来源和显示名称不能重复")
	case errors.Is(err, subscriptionstore.ErrPersonalSourceNotFound):
		writeError(w, http.StatusBadRequest, "节点来源不存在或当前用户无权访问")
	case errors.Is(err, subscriptionstore.ErrPersonalSubscriptionEmpty):
		writeError(w, http.StatusBadRequest, "个人订阅当前没有任何可用节点")
	case errors.Is(err, subscriptionstore.ErrRoutingPresetNotFound):
		writeError(w, http.StatusBadRequest, "分流方案不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanRouting):
		writeError(w, http.StatusBadRequest, "该分流方案已停用，不能用于新的个人订阅选择")
	case errors.Is(err, subscriptionstore.ErrTemplateNotFound):
		writeError(w, http.StatusBadRequest, "Mihomo 模板不存在")
	default:
		writeInternalError(w, err)
	}
}
