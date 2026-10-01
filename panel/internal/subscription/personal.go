package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

type personalSourceState struct {
	name         string
	detail       string
	status       string
	statusDetail string
	accessible   bool
	resolved     *ResolvedSubscriptionNode
}

func (s *Service) ListPersonalSubscriptions(ctx context.Context, actor PersonalSubscriptionActor) ([]PersonalSubscription, error) {
	if err := validatePersonalActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, personalSubscriptionSelect+`
		WHERE groups.owner_user_id = ? ORDER BY groups.created_at DESC, groups.id DESC`, actor.UserID)
	if err != nil {
		return nil, fmt.Errorf("list personal subscriptions: %w", err)
	}
	values := make([]PersonalSubscription, 0)
	for rows.Next() {
		value, err := scanPersonalSubscription(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan personal subscription: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate personal subscriptions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close personal subscriptions: %w", err)
	}
	for index := range values {
		values[index].Nodes, err = s.listPersonalSubscriptionNodes(ctx, actor, values[index])
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (s *Service) GetPersonalSubscription(ctx context.Context, actor PersonalSubscriptionActor, id int64) (PersonalSubscription, error) {
	if err := validatePersonalActor(actor); err != nil || id <= 0 {
		return PersonalSubscription{}, ErrPersonalSubscriptionNotFound
	}
	value, err := scanPersonalSubscription(s.db.QueryRowContext(ctx, personalSubscriptionSelect+`
		WHERE groups.id = ? AND groups.owner_user_id = ?`, id, actor.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalSubscription{}, ErrPersonalSubscriptionNotFound
	}
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("get personal subscription: %w", err)
	}
	value.Nodes, err = s.listPersonalSubscriptionNodes(ctx, actor, value)
	return value, err
}

func (s *Service) CreatePersonalSubscription(ctx context.Context, actor PersonalSubscriptionActor,
	input CreatePersonalSubscriptionInput,
) (PersonalSubscription, error) {
	if err := validatePersonalActor(actor); err != nil {
		return PersonalSubscription{}, err
	}
	name, title, clientName, err := normalizePersonalSubscriptionFields(input.Name, input.SubscriptionTitle, input.ClientName)
	if err != nil {
		return PersonalSubscription{}, err
	}
	routingID, err := resolvePlanRoutingPresetRef(ctx, s.db, input.RoutingPresetID, nil)
	if err != nil {
		return PersonalSubscription{}, err
	}
	if err := validatePlanTemplateRef(ctx, s.db, input.MihomoTemplateID); err != nil {
		return PersonalSubscription{}, err
	}
	tokenValue, _, err := token.New()
	if err != nil {
		return PersonalSubscription{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `INSERT INTO personal_subscription_groups
		(owner_user_id, name, subscription_title, token, enabled, client_name, routing_preset_id,
		 mihomo_template_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actor.UserID, name, title, tokenValue, input.Enabled, clientName, *routingID,
		nullableID(input.MihomoTemplateID), now.Unix(), now.Unix())
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("create personal subscription: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("read personal subscription id: %w", err)
	}
	return s.GetPersonalSubscription(ctx, actor, id)
}

func (s *Service) UpdatePersonalSubscription(ctx context.Context, actor PersonalSubscriptionActor, id int64,
	input UpdatePersonalSubscriptionInput,
) (PersonalSubscription, error) {
	current, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscription{}, err
	}
	name, title, clientName := current.Name, current.SubscriptionTitle, current.ClientName
	if input.Name != nil {
		name = *input.Name
	}
	if input.SubscriptionTitle != nil {
		title = *input.SubscriptionTitle
	}
	if input.ClientName != nil {
		clientName = *input.ClientName
	}
	name, title, clientName, err = normalizePersonalSubscriptionFields(name, title, clientName)
	if err != nil {
		return PersonalSubscription{}, err
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	routingID := current.RoutingPresetID
	if input.RoutingPresetID != nil {
		currentRoutingID := current.RoutingPresetID
		resolved, err := resolvePlanRoutingPresetRef(ctx, s.db, input.RoutingPresetID, &currentRoutingID)
		if err != nil {
			return PersonalSubscription{}, err
		}
		routingID = *resolved
	}
	templateID := current.MihomoTemplateID
	if input.MihomoTemplateIDSet {
		if err := validatePlanTemplateRef(ctx, s.db, input.MihomoTemplateID); err != nil {
			return PersonalSubscription{}, err
		}
		templateID = input.MihomoTemplateID
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE personal_subscription_groups
		SET name = ?, subscription_title = ?, enabled = ?, client_name = ?, routing_preset_id = ?,
		mihomo_template_id = ?, updated_at = ? WHERE id = ? AND owner_user_id = ?`,
		name, title, enabled, clientName, routingID, nullableID(templateID),
		s.now().UTC().Truncate(time.Second).Unix(), id, actor.UserID); err != nil {
		return PersonalSubscription{}, fmt.Errorf("update personal subscription: %w", err)
	}
	return s.GetPersonalSubscription(ctx, actor, id)
}

func (s *Service) DeletePersonalSubscription(ctx context.Context, actor PersonalSubscriptionActor, id int64) error {
	if err := validatePersonalActor(actor); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM personal_subscription_groups WHERE id = ? AND owner_user_id = ?`, id, actor.UserID)
	if err != nil {
		return fmt.Errorf("delete personal subscription: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read personal subscription deletion: %w", err)
	} else if count != 1 {
		return ErrPersonalSubscriptionNotFound
	}
	return nil
}

func (s *Service) SetPersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor, id int64,
	inputs []SetPersonalSubscriptionNodeInput,
) (PersonalSubscription, error) {
	group, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscription{}, err
	}
	existing := make(map[string]struct{}, len(group.Nodes))
	for _, node := range group.Nodes {
		existing[personalSourceKey(node.SourceType, node.SourceID)] = struct{}{}
	}
	normalized := make([]SetPersonalSubscriptionNodeInput, 0, len(inputs))
	seenSources := make(map[string]struct{}, len(inputs))
	seenNames := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		input.SourceType = strings.ToLower(strings.TrimSpace(input.SourceType))
		input.DisplayName = strings.TrimSpace(input.DisplayName)
		if !validPersonalSourceType(input.SourceType) || input.SourceID <= 0 || input.DisplayName == "" ||
			utf8.RuneCountInString(input.DisplayName) > maxNameRunes {
			return PersonalSubscription{}, ErrInvalidPersonalNodes
		}
		key := personalSourceKey(input.SourceType, input.SourceID)
		if _, duplicate := seenSources[key]; duplicate {
			return PersonalSubscription{}, ErrInvalidPersonalNodes
		}
		if _, duplicate := seenNames[input.DisplayName]; duplicate {
			return PersonalSubscription{}, ErrInvalidPersonalNodes
		}
		state, err := s.inspectPersonalSource(ctx, actor, input.SourceType, input.SourceID, group.ClientName, input.DisplayName, false)
		if err != nil {
			return PersonalSubscription{}, err
		}
		if !state.accessible {
			if _, wasConfigured := existing[key]; !wasConfigured {
				return PersonalSubscription{}, ErrPersonalSourceNotFound
			}
		}
		seenSources[key] = struct{}{}
		seenNames[input.DisplayName] = struct{}{}
		normalized = append(normalized, input)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("begin personal subscription node update: %w", err)
	}
	defer tx.Rollback()
	if result, err := tx.ExecContext(ctx,
		`DELETE FROM personal_subscription_nodes WHERE group_id = ? AND EXISTS (
			SELECT 1 FROM personal_subscription_groups WHERE id = ? AND owner_user_id = ?)`, id, id, actor.UserID,
	); err != nil {
		return PersonalSubscription{}, fmt.Errorf("clear personal subscription nodes: %w", err)
	} else if count, err := result.RowsAffected(); err != nil {
		return PersonalSubscription{}, fmt.Errorf("read personal subscription node clearing: %w", err)
	} else if count == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM personal_subscription_groups WHERE id = ? AND owner_user_id = ?`, id, actor.UserID,
		).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return PersonalSubscription{}, ErrPersonalSubscriptionNotFound
		} else if err != nil {
			return PersonalSubscription{}, err
		}
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	for index, input := range normalized {
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_subscription_nodes
			(group_id, source_type, source_id, display_name, enabled, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, input.SourceType, input.SourceID,
			input.DisplayName, input.Enabled, index+1, now, now); err != nil {
			return PersonalSubscription{}, fmt.Errorf("create personal subscription node: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE personal_subscription_groups SET updated_at = ? WHERE id = ? AND owner_user_id = ?`, now, id, actor.UserID,
	); err != nil {
		return PersonalSubscription{}, fmt.Errorf("touch personal subscription: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PersonalSubscription{}, fmt.Errorf("commit personal subscription node update: %w", err)
	}
	return s.GetPersonalSubscription(ctx, actor, id)
}

func (s *Service) RegeneratePersonalSubscriptionToken(ctx context.Context, actor PersonalSubscriptionActor, id int64) (PersonalSubscription, error) {
	if _, err := s.GetPersonalSubscription(ctx, actor, id); err != nil {
		return PersonalSubscription{}, err
	}
	tokenValue, _, err := token.New()
	if err != nil {
		return PersonalSubscription{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE personal_subscription_groups SET token = ?, updated_at = ?
		WHERE id = ? AND owner_user_id = ?`, tokenValue, s.now().UTC().Truncate(time.Second).Unix(), id, actor.UserID)
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("regenerate personal subscription token: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return PersonalSubscription{}, ErrPersonalSubscriptionNotFound
	}
	return s.GetPersonalSubscription(ctx, actor, id)
}

func (s *Service) ListPersonalSubscriptionSources(ctx context.Context, actor PersonalSubscriptionActor,
	clientName string,
) ([]PersonalSubscriptionSource, error) {
	if err := validatePersonalActor(actor); err != nil {
		return nil, err
	}
	clientName = strings.TrimSpace(clientName)
	if clientName == "" || utf8.RuneCountInString(clientName) > maxNameRunes {
		return nil, ErrInvalidPersonalSubscription
	}
	values := make([]PersonalSubscriptionSource, 0)
	proxies, err := s.proxies.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range proxies {
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourceProxy, value.ID, clientName, value.Name, false)
		if err != nil {
			return nil, err
		}
		if state.accessible {
			values = append(values, personalSourceResponse(PersonalSourceProxy, value.ID, value.Name, state))
		}
	}
	published, err := s.ListPublishedNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range published {
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourcePublished, value.ID, clientName, value.Name, false)
		if err != nil {
			return nil, err
		}
		if state.accessible {
			values = append(values, personalSourceResponse(PersonalSourcePublished, value.ID, value.Name, state))
		}
	}
	landings, err := s.landings.List(ctx, actor.UserID)
	if err != nil {
		return nil, err
	}
	for _, value := range landings {
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourceLanding, value.ID, clientName, value.Name, false)
		if err != nil {
			return nil, err
		}
		if state.accessible {
			values = append(values, personalSourceResponse(PersonalSourceLanding, value.ID, value.Name, state))
		}
	}
	return values, nil
}

func (s *Service) GeneratePersonalSubscriptionData(ctx context.Context, tokenValue string) (PersonalSubscriptionData, error) {
	if strings.TrimSpace(tokenValue) == "" {
		return PersonalSubscriptionData{}, ErrPersonalSubscriptionNotFound
	}
	var id, ownerID int64
	var role string
	var enabled int
	if err := s.db.QueryRowContext(ctx, `SELECT groups.id, groups.owner_user_id, users.role, groups.enabled
		FROM personal_subscription_groups AS groups JOIN users ON users.id = groups.owner_user_id
		WHERE groups.token = ?`, tokenValue).Scan(&id, &ownerID, &role, &enabled); errors.Is(err, sql.ErrNoRows) {
		return PersonalSubscriptionData{}, ErrPersonalSubscriptionNotFound
	} else if err != nil {
		return PersonalSubscriptionData{}, fmt.Errorf("find personal subscription token: %w", err)
	}
	if enabled == 0 || (role != "admin" && role != "vip") {
		return PersonalSubscriptionData{}, ErrSubscriptionUnavailable
	}
	return s.GeneratePersonalSubscriptionDataForOwner(ctx, PersonalSubscriptionActor{UserID: ownerID, Role: role}, id)
}

func (s *Service) GeneratePersonalSubscriptionDataForOwner(ctx context.Context, actor PersonalSubscriptionActor,
	id int64,
) (PersonalSubscriptionData, error) {
	group, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscriptionData{}, err
	}
	routing, err := s.GetRoutingPreset(ctx, group.RoutingPresetID)
	if err != nil {
		return PersonalSubscriptionData{}, err
	}
	result := PersonalSubscriptionData{
		Title: group.SubscriptionTitle, PublishedNodeNames: make(map[int64]string), RoutingPreset: &routing,
	}
	if result.Title == "" {
		result.Title = group.Name
	}
	if group.MihomoTemplateID != nil {
		template, err := s.GetTemplate(ctx, *group.MihomoTemplateID)
		if err != nil {
			return PersonalSubscriptionData{}, err
		}
		if template.Enabled {
			result.Template = &template
		}
	}
	for _, node := range group.Nodes {
		if !node.Enabled {
			continue
		}
		state, err := s.inspectPersonalSource(ctx, actor, node.SourceType, node.SourceID,
			group.ClientName, node.DisplayName, true)
		if err != nil {
			return PersonalSubscriptionData{}, err
		}
		if state.status != PersonalNodeReady || state.resolved == nil {
			continue
		}
		result.Nodes = append(result.Nodes, *state.resolved)
		if node.SourceType == PersonalSourcePublished {
			result.PublishedNodeNames[node.SourceID] = node.DisplayName
		}
	}
	if len(result.Nodes) == 0 {
		return PersonalSubscriptionData{}, ErrPersonalSubscriptionEmpty
	}
	return result, nil
}

func (s *Service) listPersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor,
	group PersonalSubscription,
) ([]PersonalSubscriptionNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, group_id, source_type, source_id, display_name,
		enabled, position, created_at, updated_at FROM personal_subscription_nodes
		WHERE group_id = ? ORDER BY position, id`, group.ID)
	if err != nil {
		return nil, fmt.Errorf("list personal subscription nodes: %w", err)
	}
	values := make([]PersonalSubscriptionNode, 0)
	for rows.Next() {
		var value PersonalSubscriptionNode
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(&value.ID, &value.GroupID, &value.SourceType, &value.SourceID,
			&value.DisplayName, &enabled, &value.Position, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan personal subscription node: %w", err)
		}
		value.Enabled = enabled != 0
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate personal subscription nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close personal subscription nodes: %w", err)
	}
	for index := range values {
		state, err := s.inspectPersonalSource(ctx, actor, values[index].SourceType, values[index].SourceID,
			group.ClientName, values[index].DisplayName, false)
		if err != nil {
			return nil, err
		}
		values[index].SourceName = state.name
		values[index].SourceDetail = state.detail
		values[index].Status = state.status
		values[index].StatusDetail = state.statusDetail
	}
	return values, nil
}

func (s *Service) inspectPersonalSource(ctx context.Context, actor PersonalSubscriptionActor, sourceType string,
	sourceID int64, clientName, displayName string, resolve bool,
) (personalSourceState, error) {
	switch sourceType {
	case PersonalSourceProxy:
		value, err := s.proxies.Get(ctx, sourceID)
		if errors.Is(err, proxystore.ErrNotFound) {
			return unavailablePersonalSource("本地 Proxy", PersonalNodeUnavailable, "Proxy 不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		allowed, err := s.personalCanAccessServer(ctx, actor.UserID, value.ServerID)
		if err != nil {
			return personalSourceState{}, err
		}
		if !allowed {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Proxy 不存在或不可访问"), nil
		}
		state := personalSourceState{name: value.Name, detail: value.ServerName + " · " + value.Name, accessible: true}
		if !value.Enabled {
			state.status, state.statusDetail = PersonalNodeProxyDisabled, "Proxy 已停用"
			return state, nil
		}
		client, status, detail, err := s.matchPersonalClient(ctx, actor, sourceID, clientName)
		if err != nil {
			return personalSourceState{}, err
		}
		state.status, state.statusDetail = status, detail
		if status != PersonalNodeReady || !resolve {
			return state, nil
		}
		share, err := s.proxies.GetClientShareWithOptions(ctx, client.ID, proxystore.ShareOptions{DisplayName: displayName})
		if err != nil {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Proxy 入口地址不可用"
			return state, nil
		}
		resolved := resolvedNodeFromClientShare(share)
		state.resolved = &resolved
		return state, nil

	case PersonalSourcePublished:
		value, err := s.GetPublishedNode(ctx, sourceID)
		if errors.Is(err, ErrPublishedNodeNotFound) {
			return unavailablePersonalSource("发布节点", PersonalNodeUnavailable, "发布节点不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		allowed, err := s.personalCanAccessServer(ctx, actor.UserID, value.TargetServerID)
		if err != nil {
			return personalSourceState{}, err
		}
		if allowed && value.SourceServerID != nil {
			allowed, err = s.personalCanAccessServer(ctx, actor.UserID, *value.SourceServerID)
			if err != nil {
				return personalSourceState{}, err
			}
		}
		if !allowed {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "发布节点不存在或不可访问"), nil
		}
		detail := value.TargetServerName + " · " + value.TargetProxyName
		if value.Mode == NodeModeRelay {
			detail = value.SourceServerName + " · Realm → " + detail
		}
		state := personalSourceState{name: value.Name, detail: detail, accessible: true}
		if !value.Enabled {
			state.status, state.statusDetail = PersonalNodeSourceDisabled, "发布节点已停用"
			return state, nil
		}
		proxyValue, err := s.proxies.Get(ctx, value.TargetProxyID)
		if errors.Is(err, proxystore.ErrNotFound) {
			state.status, state.statusDetail = PersonalNodeUnavailable, "目标 Proxy 不存在"
			return state, nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		if !proxyValue.Enabled {
			state.status, state.statusDetail = PersonalNodeProxyDisabled, "目标 Proxy 已停用"
			return state, nil
		}
		client, status, statusDetail, err := s.matchPersonalClient(ctx, actor, value.TargetProxyID, clientName)
		if err != nil {
			return personalSourceState{}, err
		}
		state.status, state.statusDetail = status, statusDetail
		if status != PersonalNodeReady || !resolve {
			return state, nil
		}
		if strings.TrimSpace(value.EntryAddress) == "" || value.EntryPort <= 0 {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "发布节点入口地址不可用"
			return state, nil
		}
		share, err := s.proxies.GetClientShareAtEndpointWithOptions(ctx, client.ID, proxystore.ShareEndpoint{
			Address: value.EntryAddress, Port: value.EntryPort,
		}, proxystore.ShareOptions{DisplayName: displayName})
		if err != nil {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "发布节点入口地址不可用"
			return state, nil
		}
		resolved := resolvedNodeFromClientShare(share)
		state.resolved = &resolved
		return state, nil

	case PersonalSourceLanding:
		value, err := s.landings.Get(ctx, sourceID, actor.UserID)
		if errors.Is(err, landingstore.ErrNotFound) {
			return unavailablePersonalSource("外部节点", PersonalNodeUnavailable, "外部节点不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		state := personalSourceState{
			name: value.Name, detail: fmt.Sprintf("%s · %s:%d", value.Protocol, value.Host, value.Port),
			status: PersonalNodeReady, statusDetail: "节点自带凭据", accessible: true,
		}
		if !resolve {
			return state, nil
		}
		raw, err := s.landings.GetURI(ctx, sourceID, actor.UserID)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeUnavailable, "外部节点不可用"
			return state, nil
		}
		parsed, err := landingstore.ParseURI(raw)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeUnavailable, "外部节点 URI 无效"
			return state, nil
		}
		rewritten, err := landingstore.RewriteDisplayNameURI(raw, displayName)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeUnavailable, "外部节点 URI 无效"
			return state, nil
		}
		resolved := ResolvedSubscriptionNode{
			Name: displayName, Protocol: parsed.Protocol, Address: parsed.Host, Port: parsed.Port,
			UUID: parsed.UUID, Security: parsed.Security,
			TLS:        parsed.Security == proxystore.SecurityTLS || parsed.Security == proxystore.SecurityReality,
			ServerName: parsed.ServerName, Flow: parsed.Flow, Fingerprint: parsed.Fingerprint,
			RealityPublicKey: parsed.RealityPublicKey, RealityShortID: parsed.RealityShortID,
			Method: parsed.Method, Network: parsed.Network, ShadowsocksPassword: parsed.Password, URI: rewritten,
		}
		state.resolved = &resolved
		return state, nil
	default:
		return personalSourceState{}, ErrInvalidPersonalNodes
	}
}

func (s *Service) matchPersonalClient(ctx context.Context, actor PersonalSubscriptionActor, proxyID int64,
	clientName string,
) (proxystore.Client, string, string, error) {
	assignment := `clients.assigned_user_id = ?`
	if actor.Role == "admin" {
		assignment = `(clients.assigned_user_id IS NULL OR clients.assigned_user_id = ?)`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT clients.id FROM clients
		WHERE clients.proxy_id = ? AND clients.name = ? COLLATE BINARY AND `+assignment+`
		AND NOT EXISTS (SELECT 1 FROM subscriber_clients WHERE subscriber_clients.client_id = clients.id)
		ORDER BY clients.id`, proxyID, clientName, actor.UserID)
	if err != nil {
		return proxystore.Client{}, "", "", fmt.Errorf("match personal subscription client: %w", err)
	}
	ids := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return proxystore.Client{}, "", "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return proxystore.Client{}, "", "", err
	}
	if err := rows.Close(); err != nil {
		return proxystore.Client{}, "", "", err
	}
	if len(ids) == 0 {
		return proxystore.Client{}, PersonalNodeMissing, "未找到同名 Client", nil
	}
	if len(ids) > 1 {
		return proxystore.Client{}, PersonalNodeAmbiguous, "找到多个同名 Client", nil
	}
	client, err := s.proxies.GetClient(ctx, ids[0])
	if errors.Is(err, proxystore.ErrClientNotFound) {
		return proxystore.Client{}, PersonalNodeMissing, "未找到同名 Client", nil
	}
	if err != nil {
		return proxystore.Client{}, "", "", err
	}
	if client.SubscriptionManaged {
		return proxystore.Client{}, PersonalNodeMissing, "未找到可用的非订阅托管 Client", nil
	}
	lifecycle := client.LifecycleAt(s.now())
	switch {
	case !client.Enabled:
		return proxystore.Client{}, PersonalNodeClientDisabled, "Client 已停用", nil
	case lifecycle.Expired:
		return proxystore.Client{}, PersonalNodeClientExpired, "Client 已到期", nil
	case lifecycle.QuotaExhausted:
		return proxystore.Client{}, PersonalNodeClientExhausted, "Client 流量已用完", nil
	case !lifecycle.EffectiveEnabled:
		return proxystore.Client{}, PersonalNodeClientDisabled, "Client 当前不可用", nil
	default:
		return client, PersonalNodeReady, "可用", nil
	}
}

func (s *Service) personalCanAccessServer(ctx context.Context, userID, serverID int64) (bool, error) {
	var allowed bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM servers
		WHERE id = ? AND archived_at IS NULL AND (visibility = 'public' OR EXISTS (
			SELECT 1 FROM server_access WHERE server_access.server_id = servers.id AND server_access.user_id = ?)))`,
		serverID, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check personal subscription server access: %w", err)
	}
	return allowed, nil
}

func personalSourceResponse(sourceType string, sourceID int64, defaultName string,
	state personalSourceState,
) PersonalSubscriptionSource {
	return PersonalSubscriptionSource{
		SourceType: sourceType, SourceID: sourceID, Name: state.name, Detail: state.detail,
		DefaultName: defaultName, Status: state.status, StatusDetail: state.statusDetail,
	}
}

func unavailablePersonalSource(name, status, detail string) personalSourceState {
	return personalSourceState{name: name, status: status, statusDetail: detail}
}

func normalizePersonalSubscriptionFields(name, title, clientName string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	title = strings.TrimSpace(title)
	clientName = strings.TrimSpace(clientName)
	if name == "" || clientName == "" || utf8.RuneCountInString(name) > maxNameRunes ||
		utf8.RuneCountInString(title) > maxNameRunes || utf8.RuneCountInString(clientName) > maxNameRunes {
		return "", "", "", ErrInvalidPersonalSubscription
	}
	return name, title, clientName, nil
}

func validatePersonalActor(actor PersonalSubscriptionActor) error {
	if actor.UserID <= 0 || (actor.Role != "admin" && actor.Role != "vip") {
		return ErrPersonalSubscriptionNotFound
	}
	return nil
}

func validPersonalSourceType(value string) bool {
	return value == PersonalSourceProxy || value == PersonalSourcePublished || value == PersonalSourceLanding
}

func personalSourceKey(sourceType string, sourceID int64) string {
	return fmt.Sprintf("%s:%d", sourceType, sourceID)
}

const personalSubscriptionSelect = `SELECT groups.id, groups.owner_user_id, groups.name,
	groups.subscription_title, groups.token, groups.enabled, groups.client_name,
	groups.routing_preset_id, routing.name, groups.mihomo_template_id, COALESCE(templates.name, ''),
	groups.created_at, groups.updated_at
	FROM personal_subscription_groups AS groups
	JOIN subscription_routing_presets AS routing ON routing.id = groups.routing_preset_id
	LEFT JOIN subscription_templates AS templates ON templates.id = groups.mihomo_template_id `

func scanPersonalSubscription(row rowScanner) (PersonalSubscription, error) {
	var value PersonalSubscription
	var enabled int
	var templateID sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.OwnerUserID, &value.Name, &value.SubscriptionTitle, &value.Token,
		&enabled, &value.ClientName, &value.RoutingPresetID, &value.RoutingPresetName,
		&templateID, &value.MihomoTemplateName, &createdAt, &updatedAt); err != nil {
		return PersonalSubscription{}, err
	}
	if templateID.Valid {
		id := templateID.Int64
		value.MihomoTemplateID = &id
	}
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
