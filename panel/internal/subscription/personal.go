package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"github.com/renaissance0721/vps-panel/panel/internal/noderole"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

type personalSourceState struct {
	name           string
	detail         string
	status         string
	statusDetail   string
	accessible     bool
	selectable     bool // Eligibility for a new node, independent of runtime readiness.
	requiresClient bool
	resolved       *ResolvedSubscriptionNode
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
	if err := validatePlanTemplateRef(ctx, s.db, input.MihomoTemplateID, nil, TemplateTypeMihomo); err != nil {
		return PersonalSubscription{}, err
	}
	if err := validatePlanTemplateRef(ctx, s.db, input.ShadowrocketTemplateID, nil, TemplateTypeShadowrocket); err != nil {
		return PersonalSubscription{}, err
	}
	tokenValue, _, err := token.New()
	if err != nil {
		return PersonalSubscription{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `INSERT INTO personal_subscription_groups
		(owner_user_id, name, subscription_title, token, enabled, client_name, routing_preset_id,
		 mihomo_template_id, shadowrocket_template_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actor.UserID, name, title, tokenValue, input.Enabled, clientName, *routingID,
		nullableID(input.MihomoTemplateID), nullableID(input.ShadowrocketTemplateID), now.Unix(), now.Unix())
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
		if err := validatePlanTemplateRef(ctx, s.db, input.MihomoTemplateID, current.MihomoTemplateID, TemplateTypeMihomo); err != nil {
			return PersonalSubscription{}, err
		}
		templateID = input.MihomoTemplateID
	}
	shadowrocketTemplateID := current.ShadowrocketTemplateID
	if input.ShadowrocketTemplateIDSet {
		if err := validatePlanTemplateRef(ctx, s.db, input.ShadowrocketTemplateID, current.ShadowrocketTemplateID, TemplateTypeShadowrocket); err != nil {
			return PersonalSubscription{}, err
		}
		shadowrocketTemplateID = input.ShadowrocketTemplateID
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE personal_subscription_groups
		SET name = ?, subscription_title = ?, enabled = ?, client_name = ?, routing_preset_id = ?,
		shadowrocket_defaults_json = CASE WHEN routing_preset_id = ? THEN shadowrocket_defaults_json ELSE '{}' END,
		mihomo_template_id = ?, shadowrocket_template_id = ?, updated_at = ? WHERE id = ? AND owner_user_id = ?`,
		name, title, enabled, clientName, routingID, routingID, nullableID(templateID), nullableID(shadowrocketTemplateID),
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

func (s *Service) normalizePersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor, group PersonalSubscription, groups []RoutingGroup,
	inputs []SetPersonalSubscriptionNodeInput, preview bool,
) ([]SetPersonalSubscriptionNodeInput, error) {
	existing := make(map[int64]PersonalSubscriptionNode, len(group.Nodes))
	for _, node := range group.Nodes {
		existing[node.ID] = node
	}
	normalized := make([]SetPersonalSubscriptionNodeInput, 0, len(inputs))
	seenNames := make(map[string]struct{}, len(inputs))
	seenIDs := make(map[int64]struct{}, len(inputs))
	reserved := personalReservedNodeNames(groups)
	for _, input := range inputs {
		var previousNode PersonalSubscriptionNode
		if preview && input.ID == nil {
			return nil, ErrInvalidPersonalNodes
		}
		if input.ID != nil {
			if *input.ID == 0 || (*input.ID < 0 && !preview) {
				return nil, ErrInvalidPersonalNodes
			}
			if _, exists := existing[*input.ID]; *input.ID > 0 && !exists {
				return nil, ErrInvalidPersonalNodes
			}
			if _, duplicate := seenIDs[*input.ID]; duplicate {
				return nil, ErrInvalidPersonalNodes
			}
			seenIDs[*input.ID] = struct{}{}
			previousNode = existing[*input.ID]
		}
		input.SourceType = strings.ToLower(strings.TrimSpace(input.SourceType))
		input.DisplayName = strings.TrimSpace(input.DisplayName)
		if input.DialerProxy == nil {
			previous := ""
			if previousNode.SourceType == input.SourceType && previousNode.SourceID == input.SourceID {
				previous = previousNode.DialerProxy
			}
			input.DialerProxy = &previous
		}
		upstream := strings.TrimSpace(*input.DialerProxy)
		if !utf8.ValidString(upstream) || utf8.RuneCountInString(upstream) > maxNameRunes || strings.IndexFunc(upstream, unicode.IsControl) >= 0 || upstream == "DIRECT" || upstream == "REJECT" ||
			(upstream != "" && (input.SourceType == PersonalSourceRemoteSource || input.SourceType == PersonalSourceRemoteNode)) {
			return nil, ErrInvalidPersonalNodes
		}
		input.DialerProxy = &upstream
		if !validPersonalSourceType(input.SourceType) || input.SourceID <= 0 {
			return nil, ErrInvalidPersonalNodes
		}
		if input.DisplayName != "" {
			if _, duplicate := seenNames[input.DisplayName]; duplicate {
				return nil, ErrInvalidPersonalNodes
			}
			if previousNode.CustomName != input.DisplayName && (!validPersonalCustomName(input.DisplayName) ||
				(input.SourceType != PersonalSourceRemoteSource && reserved[input.DisplayName])) {
				return nil, ErrInvalidPersonalNodes
			}
			seenNames[input.DisplayName] = struct{}{}
		}
		host, port, err := normalizePersonalNodeEndpoint(input.EntryHost, input.EntryPort)
		if err != nil {
			return nil, err
		}
		input.EntryHost, input.EntryPort = host, port
		if (input.SourceType == PersonalSourceRemoteSource || input.SourceType == PersonalSourceRemoteNode) && (input.EntryHost != nil || input.EntryPort != nil) {
			return nil, ErrInvalidPersonalNodes
		}
		state, err := s.inspectPersonalSource(ctx, actor, input.SourceType, input.SourceID, group.ClientName,
			input.DisplayName, input.EntryHost, input.EntryPort, false)
		if err != nil {
			return nil, err
		}
		if !state.accessible || !state.selectable {
			if previousNode.SourceType != input.SourceType || previousNode.SourceID != input.SourceID {
				return nil, ErrPersonalSourceNotFound
			}
		}
		input.sourceName = state.name
		normalized = append(normalized, input)
	}
	// Upstreams are policy groups, never one of this subscription's nodes.
	for _, input := range normalized {
		if *input.DialerProxy != "" {
			if _, exists := seenNames[*input.DialerProxy]; exists {
				return nil, ErrInvalidPersonalNodes
			}
		}
	}
	return normalized, nil
}

func (s *Service) SetPersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor, id int64,
	inputs []SetPersonalSubscriptionNodeInput,
) (PersonalSubscription, error) {
	group, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscription{}, err
	}
	normalized, err := s.normalizePersonalSubscriptionNodes(ctx, actor, group, group.routingGroups, inputs, false)
	if err != nil {
		return PersonalSubscription{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("begin personal subscription node update: %w", err)
	}
	defer tx.Rollback()
	positionOffset := len(group.Nodes) + len(normalized) + 1000
	if _, err := tx.ExecContext(ctx, `UPDATE personal_subscription_nodes
		SET position = position + ?, display_name = NULL WHERE group_id = ?`,
		positionOffset, id); err != nil {
		return PersonalSubscription{}, fmt.Errorf("prepare personal subscription node update: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	keptIDs := make(map[int64]struct{}, len(normalized))
	savedIDs := make([]int64, 0, len(normalized))
	for index, input := range normalized {
		if input.ID != nil {
			result, err := tx.ExecContext(ctx, `UPDATE personal_subscription_nodes SET
				source_type = ?, source_id = ?, display_name = NULLIF(?, ''), enabled = ?, position = ?,
				entry_host = ?, entry_port = ?, dialer_proxy = ?, updated_at = ? WHERE id = ? AND group_id = ?`,
				input.SourceType, input.SourceID, input.DisplayName, input.Enabled, index+1,
				nullablePersonalString(input.EntryHost), nullableInt(input.EntryPort), *input.DialerProxy, now, *input.ID, id)
			if err != nil {
				return PersonalSubscription{}, fmt.Errorf("update personal subscription node: %w", err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				return PersonalSubscription{}, ErrInvalidPersonalNodes
			}
			keptIDs[*input.ID] = struct{}{}
			savedIDs = append(savedIDs, *input.ID)
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO personal_subscription_nodes
			(group_id, source_type, source_id, display_name, enabled, position, entry_host, entry_port, dialer_proxy, created_at, updated_at)
			VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`, id, input.SourceType, input.SourceID,
			input.DisplayName, input.Enabled, index+1, nullablePersonalString(input.EntryHost), nullableInt(input.EntryPort), *input.DialerProxy, now, now)
		if err != nil {
			return PersonalSubscription{}, fmt.Errorf("create personal subscription node: %w", err)
		}
		createdID, err := result.LastInsertId()
		if err != nil {
			return PersonalSubscription{}, fmt.Errorf("read personal subscription node id: %w", err)
		}
		keptIDs[createdID] = struct{}{}
		savedIDs = append(savedIDs, createdID)
	}
	for _, node := range group.Nodes {
		existingID := node.ID
		if _, keep := keptIDs[existingID]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM personal_subscription_nodes WHERE id = ? AND group_id = ?`, existingID, id); err != nil {
			return PersonalSubscription{}, fmt.Errorf("delete personal subscription node: %w", err)
		}
	}
	bindings := pruneRoutingBindings(group.RoutingBindings, keptIDs)
	bindingsJSON, err := encodeRoutingBindings(bindings)
	if err != nil {
		return PersonalSubscription{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE personal_subscription_groups SET routing_bindings_json = ?, updated_at = ?
		 WHERE id = ? AND owner_user_id = ?`, bindingsJSON, now, id, actor.UserID,
	); err != nil {
		return PersonalSubscription{}, fmt.Errorf("touch personal subscription: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PersonalSubscription{}, fmt.Errorf("commit personal subscription node update: %w", err)
	}
	saved, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscription{}, err
	}
	// The editor maps new instance IDs using the order of this write.
	if len(saved.Nodes) != len(savedIDs) {
		return PersonalSubscription{}, ErrPersonalNodesChanged
	}
	for i, node := range saved.Nodes {
		if node.ID != savedIDs[i] {
			return PersonalSubscription{}, ErrPersonalNodesChanged
		}
	}
	return saved, nil
}

func (s *Service) SetPersonalSubscriptionRoutingBindings(ctx context.Context, actor PersonalSubscriptionActor,
	id int64, bindings RoutingBindings,
) (PersonalSubscription, error) {
	return s.SetPersonalSubscriptionRouting(ctx, actor, id, bindings, nil)
}

func (s *Service) SetPersonalSubscriptionRouting(ctx context.Context, actor PersonalSubscriptionActor,
	id int64, bindings RoutingBindings, updates ShadowrocketDefaultUpdates,
) (PersonalSubscription, error) {
	group, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscription{}, err
	}
	preset, err := s.GetRoutingPreset(ctx, group.RoutingPresetID)
	if err != nil {
		return PersonalSubscription{}, err
	}
	allowed := make(map[int64]struct{}, len(group.Nodes))
	for _, node := range group.Nodes {
		allowed[node.ID] = struct{}{}
	}
	defaultsOnly := bindings == nil && len(updates) > 0
	if defaultsOnly {
		bindings = group.RoutingBindings
	}
	normalized, err := normalizeRoutingBindings(bindings, preset.Groups, allowed)
	if err != nil {
		return PersonalSubscription{}, err
	}
	encoded, err := encodeRoutingBindings(normalized)
	if err != nil {
		return PersonalSubscription{}, err
	}
	var nextBindings any = encoded
	var previousBindings any
	if defaultsOnly {
		nextBindings, previousBindings = nil, string(encoded)
	}
	var previous, next any
	if len(updates) > 0 {
		group.RoutingBindings = normalized
		defaults, err := s.applyShadowrocketDefaultUpdates(ctx, actor, group, preset, updates)
		if err != nil {
			return PersonalSubscription{}, err
		}
		oldJSON, err := json.Marshal(group.ShadowrocketDefaults)
		if err != nil {
			return PersonalSubscription{}, err
		}
		newJSON, err := json.Marshal(defaults)
		if err != nil {
			return PersonalSubscription{}, err
		}
		previous, next = string(oldJSON), string(newJSON)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE personal_subscription_groups
		SET routing_bindings_json = COALESCE(?, routing_bindings_json), shadowrocket_defaults_json = COALESCE(?, shadowrocket_defaults_json), updated_at = ?
		WHERE id = ? AND owner_user_id = ? AND routing_preset_id = ?
		  AND (? IS NULL OR shadowrocket_defaults_json = ?)
		  AND (? IS NULL OR CAST(routing_bindings_json AS TEXT) = ?)`,
		nextBindings, next, s.now().UTC().Truncate(time.Second).Unix(), id, actor.UserID, preset.ID, previous, previous, previousBindings, previousBindings)
	if err != nil {
		return PersonalSubscription{}, fmt.Errorf("update personal subscription routing bindings: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return PersonalSubscription{}, err
	} else if count != 1 {
		if _, err := s.GetPersonalSubscription(ctx, actor, id); err != nil {
			return PersonalSubscription{}, err
		}
		return PersonalSubscription{}, ErrShadowrocketDefaultsChanged
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
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourceProxy, value.ID, clientName, value.Name, nil, nil, false)
		if err != nil {
			return nil, err
		}
		if state.accessible && state.selectable {
			values = append(values, personalSourceResponse(PersonalSourceProxy, value.ID, value.Name, state))
		}
	}
	relays, err := s.relays.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range relays {
		if value.SubscriptionPublished {
			continue
		}
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourceRelay, value.ID, clientName, value.Name, nil, nil, false)
		if err != nil {
			return nil, err
		}
		if state.accessible && state.selectable {
			values = append(values, personalSourceResponse(PersonalSourceRelay, value.ID, value.Name, state))
		}
	}
	landings, err := s.landings.List(ctx, actor.UserID)
	if err != nil {
		return nil, err
	}
	for _, value := range landings {
		state, err := s.inspectPersonalSource(ctx, actor, PersonalSourceLanding, value.ID, clientName, value.Name, nil, nil, false)
		if err != nil {
			return nil, err
		}
		if state.accessible && state.selectable {
			values = append(values, personalSourceResponse(PersonalSourceLanding, value.ID, value.Name, state))
		}
	}
	remote, err := s.listPersonalRemoteSources(ctx, actor)
	if err != nil {
		return nil, err
	}
	return append(values, remote...), nil
}

func (s *Service) GeneratePersonalSubscriptionData(ctx context.Context, tokenValue string) (PersonalSubscriptionData, error) {
	return s.generatePersonalSubscriptionData(ctx, tokenValue, false)
}

func (s *Service) GeneratePersonalMihomoProviderData(ctx context.Context, tokenValue string) (PersonalSubscriptionData, error) {
	return s.generatePersonalSubscriptionData(ctx, tokenValue, true)
}

func (s *Service) generatePersonalSubscriptionData(ctx context.Context, tokenValue string, localOnly bool) (PersonalSubscriptionData, error) {
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
	return s.generatePersonalSubscriptionDataForOwner(ctx, PersonalSubscriptionActor{UserID: ownerID, Role: role}, id, localOnly)
}

func (s *Service) GeneratePersonalSubscriptionDataForOwner(ctx context.Context, actor PersonalSubscriptionActor,
	id int64,
) (PersonalSubscriptionData, error) {
	return s.generatePersonalSubscriptionDataForOwner(ctx, actor, id, false)
}

func (s *Service) generatePersonalSubscriptionDataForOwner(ctx context.Context, actor PersonalSubscriptionActor,
	id int64, localOnly bool,
) (PersonalSubscriptionData, error) {
	group, err := s.GetPersonalSubscription(ctx, actor, id)
	if err != nil {
		return PersonalSubscriptionData{}, err
	}
	routing, err := s.GetRoutingPreset(ctx, group.RoutingPresetID)
	if err != nil {
		return PersonalSubscriptionData{}, err
	}
	result, err := s.resolvePersonalSubscriptionNodes(ctx, actor, group, routing, localOnly)
	if err != nil {
		return PersonalSubscriptionData{}, err
	}
	if len(result.Nodes) == 0 {
		return PersonalSubscriptionData{}, ErrPersonalSubscriptionEmpty
	}
	if group.MihomoTemplateID != nil {
		template, err := s.GetTemplate(ctx, *group.MihomoTemplateID)
		if err != nil {
			return PersonalSubscriptionData{}, err
		}
		if template.Enabled {
			result.MihomoTemplate = &template
		}
	}
	if group.ShadowrocketTemplateID != nil {
		template, err := s.GetTemplate(ctx, *group.ShadowrocketTemplateID)
		if err != nil {
			return PersonalSubscriptionData{}, err
		}
		if template.Enabled {
			result.ShadowrocketTemplate = &template
		}
	}
	return result, nil
}

func (s *Service) resolvePersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor,
	group PersonalSubscription, routing RoutingPreset, localOnly bool,
) (PersonalSubscriptionData, error) {
	result := PersonalSubscriptionData{
		Title: group.SubscriptionTitle, NodeNames: make(map[int64]string), NodeGroupNames: make(map[int64][]string),
		RoutingBindings: group.RoutingBindings, RoutingPreset: &routing, ShadowrocketDefaults: group.ShadowrocketDefaults,
	}
	if result.Title == "" {
		result.Title = group.Name
	}
	resolvePersonalDisplayNames(group.Nodes, routing.Groups)
	usedNames := personalReservedNodeNames(routing.Groups)
	for _, node := range group.Nodes {
		if node.Enabled && node.SourceType != PersonalSourceRemoteSource {
			usedNames[node.DisplayName] = true
		}
	}
	for _, node := range group.Nodes {
		if !node.Enabled {
			continue
		}
		if localOnly && (node.SourceType == PersonalSourceRemoteSource || node.SourceType == PersonalSourceRemoteNode) {
			continue
		}
		if node.SourceType == PersonalSourceRemoteSource || node.SourceType == PersonalSourceRemoteNode {
			var sourceID, nodeID int64
			if node.SourceType == PersonalSourceRemoteSource {
				sourceID = node.SourceID
				result.NodeGroupNames[node.ID] = []string{}
			} else {
				nodeID = node.SourceID
			}
			remoteNodes, err := s.personalRemoteNodes(ctx, actor.UserID, sourceID, nodeID, true)
			if err != nil {
				return PersonalSubscriptionData{}, err
			}
			for _, remote := range remoteNodes {
				name := node.DisplayName
				if sourceID > 0 {
					base := personalSourceNodeName(remote.name)
					name = base
					if usedNames[name] {
						name = fmt.Sprintf("%s [%d]", name, remote.id)
					}
					for suffix := 2; usedNames[name]; suffix++ {
						name = fmt.Sprintf("%s [%d-%d]", base, remote.id, suffix)
					}
					usedNames[name] = true
				}
				result.Nodes = append(result.Nodes, ResolvedSubscriptionNode{Name: name, Protocol: remote.protocol, RemoteFormat: remote.format, RemotePayload: remote.payload})
				if sourceID > 0 {
					result.NodeGroupNames[node.ID] = append(result.NodeGroupNames[node.ID], name)
				} else {
					result.NodeNames[node.ID] = name
				}
			}
			continue
		}
		state, err := s.inspectPersonalSource(ctx, actor, node.SourceType, node.SourceID,
			group.ClientName, node.DisplayName, node.EntryHost, node.EntryPort, true)
		if err != nil {
			return PersonalSubscriptionData{}, err
		}
		if state.status != PersonalNodeReady || state.resolved == nil {
			continue
		}
		state.resolved.DialerProxy = node.DialerProxy
		result.Nodes = append(result.Nodes, *state.resolved)
		result.NodeNames[node.ID] = state.resolved.Name
	}
	return result, nil
}

func (s *Service) listPersonalSubscriptionNodes(ctx context.Context, actor PersonalSubscriptionActor,
	group PersonalSubscription,
) ([]PersonalSubscriptionNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, group_id, source_type, source_id, COALESCE(display_name, ''),
		enabled, position, entry_host, entry_port, dialer_proxy, created_at, updated_at FROM personal_subscription_nodes
		WHERE group_id = ? ORDER BY position, id`, group.ID)
	if err != nil {
		return nil, fmt.Errorf("list personal subscription nodes: %w", err)
	}
	values := make([]PersonalSubscriptionNode, 0)
	for rows.Next() {
		var value PersonalSubscriptionNode
		var enabled int
		var entryHost sql.NullString
		var entryPort sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&value.ID, &value.GroupID, &value.SourceType, &value.SourceID,
			&value.CustomName, &enabled, &value.Position, &entryHost, &entryPort, &value.DialerProxy, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan personal subscription node: %w", err)
		}
		value.Enabled = enabled != 0
		if entryHost.Valid {
			host := entryHost.String
			value.EntryHost = &host
		}
		if entryPort.Valid {
			port := int(entryPort.Int64)
			value.EntryPort = &port
		}
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
			group.ClientName, values[index].CustomName, values[index].EntryHost, values[index].EntryPort, false)
		if err != nil {
			return nil, err
		}
		values[index].SourceName = state.name
		values[index].SourceDetail = state.detail
		values[index].Status = state.status
		values[index].StatusDetail = state.statusDetail
		values[index].RequiresClient = state.requiresClient
	}
	resolvePersonalDisplayNames(values, group.routingGroups)
	return values, nil
}

func (s *Service) inspectPersonalSource(ctx context.Context, actor PersonalSubscriptionActor, sourceType string,
	sourceID int64, clientName, displayName string, entryHost *string, entryPort *int, resolve bool,
) (personalSourceState, error) {
	switch sourceType {
	case PersonalSourceRemoteSource, PersonalSourceRemoteNode:
		return s.inspectPersonalRemote(ctx, actor, sourceType, sourceID)
	case PersonalSourceProxy:
		value, err := s.proxies.Get(ctx, sourceID)
		if errors.Is(err, proxystore.ErrNotFound) {
			return unavailablePersonalSource("本地 Proxy", PersonalNodeUnavailable, "Proxy 不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		serverStatus, err := s.personalAccessibleServerStatus(ctx, actor.UserID, value.ServerID)
		if err != nil {
			return personalSourceState{}, err
		}
		if serverStatus == "" {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Proxy 不存在或不可访问"), nil
		}
		state := personalSourceState{name: value.Name, detail: value.ServerName + " · " + value.Name + " · " + personalNodeRoleLabel(value.NodeRole),
			accessible: true, selectable: value.Enabled, requiresClient: true}
		if displayName == "" {
			displayName = personalSourceNodeName(value.Name)
		}
		if !value.Enabled {
			state.status, state.statusDetail = PersonalNodeProxyDisabled, "Proxy 已停用"
			return state, nil
		}
		client, status, detail, err := s.matchPersonalClient(ctx, actor, sourceID, clientName)
		if err != nil {
			return personalSourceState{}, err
		}
		state.status, state.statusDetail = status, detail
		if status != PersonalNodeReady {
			return state, nil
		}
		if serverStatus != "online" {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Proxy 服务器当前离线或尚未连接"
			return state, nil
		}
		share, err := s.resolvePersonalClientShare(ctx, client.ID, displayName,
			proxystore.ShareEndpoint{Address: value.EntryAddress, Port: value.ListenPort}, entryHost, entryPort)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Proxy 入口地址不可用"
			return state, nil
		}
		if resolve {
			resolved := resolvedNodeFromClientShare(share)
			state.resolved = &resolved
		}
		return state, nil

	case PersonalSourceRelay:
		value, err := s.relays.Get(ctx, sourceID)
		if errors.Is(err, relaystore.ErrNotFound) {
			return unavailablePersonalSource("中转 Relay", PersonalNodeUnavailable, "Relay 不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		if value.OwnerUserID != nil && actor.Role != "admin" {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Relay 不存在或不可访问"), nil
		}
		serverStatus, err := s.personalAccessibleServerStatus(ctx, actor.UserID, value.ServerID)
		if err != nil {
			return personalSourceState{}, err
		}
		if serverStatus == "" {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Relay 不存在或不可访问"), nil
		}
		if value.SubscriptionPublished {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "订阅发布中转不能用于个人订阅，请改用本地中转。"), nil
		}
		if value.TargetType == relaystore.TargetManual {
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "手动目标 Relay 不提供订阅凭据"), nil
		}
		state := personalSourceState{
			name: value.Name, detail: value.ServerName + " · " + value.Name,
			accessible: true, selectable: value.Enabled, requiresClient: value.TargetType == relaystore.TargetProxy,
		}
		if displayName == "" {
			displayName = personalSourceNodeName(value.Name)
		}
		endpoint := &proxystore.ShareEndpoint{Address: value.EntryAddress, Port: value.ListenPort}
		if !value.Enabled {
			state.status, state.statusDetail = PersonalNodeSourceDisabled, "Relay 已停用"
			return state, nil
		}
		switch value.TargetType {
		case relaystore.TargetProxy:
			if value.TargetProxyID == nil {
				state.status, state.statusDetail = PersonalNodeUnavailable, "目标 Proxy 不存在"
				return state, nil
			}
			proxyValue, err := s.proxies.Get(ctx, *value.TargetProxyID)
			if errors.Is(err, proxystore.ErrNotFound) {
				state.status, state.statusDetail = PersonalNodeUnavailable, "目标 Proxy 不存在"
				return state, nil
			}
			if err != nil {
				return personalSourceState{}, err
			}
			targetStatus, err := s.personalAccessibleServerStatus(ctx, actor.UserID, proxyValue.ServerID)
			if err != nil {
				return personalSourceState{}, err
			}
			if targetStatus == "" {
				return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Relay 不存在或不可访问"), nil
			}
			state.detail = value.ServerName + " · " + value.Name + " → " + proxyValue.ServerName + " · " + proxyValue.Name
			if !proxyValue.Enabled {
				state.status, state.statusDetail = PersonalNodeProxyDisabled, "目标 Proxy 已停用"
				return state, nil
			}
			client, status, statusDetail, err := s.matchPersonalClient(ctx, actor, *value.TargetProxyID, clientName)
			if err != nil {
				return personalSourceState{}, err
			}
			state.status, state.statusDetail = status, statusDetail
			if status != PersonalNodeReady {
				return state, nil
			}
			if serverStatus != "online" || targetStatus != "online" {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 源或目标服务器当前离线或尚未连接"
				return state, nil
			}
			if !value.TargetAddressReady {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 目标地址不可用"
				return state, nil
			}
			share, err := s.resolvePersonalClientShare(ctx, client.ID, displayName, *endpoint, entryHost, entryPort)
			if err != nil {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 入口地址不可用"
				return state, nil
			}
			if resolve {
				resolved := resolvedNodeFromClientShare(share)
				state.resolved = &resolved
			}
			return state, nil

		case relaystore.TargetLanding:
			if value.TargetLandingID == nil {
				state.status, state.statusDetail = PersonalNodeUnavailable, "目标外部节点不存在"
				return state, nil
			}
			landingValue, err := s.landings.Get(ctx, *value.TargetLandingID, actor.UserID)
			if errors.Is(err, landingstore.ErrNotFound) {
				return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Relay 不存在或不可访问"), nil
			}
			if err != nil {
				return personalSourceState{}, err
			}
			state.detail = value.ServerName + " · " + value.Name + " → " + landingValue.Name
			state.status, state.statusDetail = PersonalNodeReady, "目标外部节点自带凭据"
			if serverStatus != "online" {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 源服务器当前离线或尚未连接"
				return state, nil
			}
			if !value.TargetAddressReady {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 目标地址不可用"
				return state, nil
			}
			raw, err := s.landings.GetURI(ctx, landingValue.ID, actor.UserID)
			if err != nil {
				state.status, state.statusDetail = PersonalNodeUnavailable, "目标外部节点不可用"
				return state, nil
			}
			resolved, err := resolvePersonalLandingNode(raw, displayName, endpoint, entryHost, entryPort)
			if err != nil {
				state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "Relay 入口地址不可用"
				return state, nil
			}
			if resolve {
				state.resolved = &resolved
			}
			return state, nil
		default:
			return unavailablePersonalSource(value.Name, PersonalNodeUnavailable, "Relay 不提供可用订阅凭据"), nil
		}

	case PersonalSourceLanding:
		value, err := s.landings.Get(ctx, sourceID, actor.UserID)
		if errors.Is(err, landingstore.ErrNotFound) {
			return unavailablePersonalSource("外部节点", PersonalNodeUnavailable, "外部节点不存在或不可访问"), nil
		}
		if err != nil {
			return personalSourceState{}, err
		}
		state := personalSourceState{
			name: value.Name, detail: fmt.Sprintf("%s · %s:%d · %s", value.Protocol, value.Host, value.Port, personalNodeRoleLabel(value.NodeRole)),
			status: PersonalNodeReady, statusDetail: "节点自带凭据", accessible: true, selectable: true,
		}
		if displayName == "" {
			displayName = personalSourceNodeName(value.Name)
		}
		raw, err := s.landings.GetURI(ctx, sourceID, actor.UserID)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeUnavailable, "外部节点不可用"
			return state, nil
		}
		resolved, err := resolvePersonalLandingNode(raw, displayName, nil, entryHost, entryPort)
		if err != nil {
			state.status, state.statusDetail = PersonalNodeEndpointUnavailable, "外部节点入口地址不可用"
			return state, nil
		}
		if resolve {
			state.resolved = &resolved
		}
		return state, nil
	default:
		return personalSourceState{}, ErrInvalidPersonalNodes
	}
}

func personalNodeRoleLabel(role string) string {
	if role == noderole.Landing {
		return "落地用途"
	}
	return "直连用途"
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

func (s *Service) resolvePersonalClientShare(ctx context.Context, clientID int64, displayName string,
	endpoint proxystore.ShareEndpoint, entryHost *string, entryPort *int,
) (proxystore.ClientShare, error) {
	options := proxystore.ShareOptions{DisplayName: displayName}
	if entryHost != nil {
		endpoint.Address = *entryHost
	}
	if entryPort != nil {
		endpoint.Port = *entryPort
	}
	return s.proxies.GetClientShareAtEndpointWithOptions(ctx, clientID, endpoint, options)
}

func resolvePersonalLandingNode(raw, displayName string, defaultEndpoint *proxystore.ShareEndpoint,
	entryHost *string, entryPort *int,
) (ResolvedSubscriptionNode, error) {
	parsed, err := landingstore.ParseURI(raw)
	if err != nil {
		return ResolvedSubscriptionNode{}, err
	}
	address, port := parsed.Host, parsed.Port
	if defaultEndpoint != nil {
		address, port = defaultEndpoint.Address, defaultEndpoint.Port
	}
	if entryHost != nil {
		address = *entryHost
	}
	if entryPort != nil {
		port = *entryPort
	}
	if address != parsed.Host || port != parsed.Port {
		raw, err = landingstore.RewriteLandingURI(raw, landingstore.ShareEndpoint{Address: address, Port: port}, "")
		if err != nil {
			return ResolvedSubscriptionNode{}, err
		}
		parsed, err = landingstore.ParseURI(raw)
		if err != nil {
			return ResolvedSubscriptionNode{}, err
		}
	}
	rewritten, err := landingstore.RewriteDisplayNameURI(raw, displayName)
	if err != nil {
		return ResolvedSubscriptionNode{}, err
	}
	node := ResolvedSubscriptionNode{
		Name: displayName, Protocol: parsed.Protocol, Address: parsed.Host, Port: parsed.Port,
		UUID: parsed.UUID, Security: parsed.Security,
		TLS:        parsed.Security == proxystore.SecurityTLS || parsed.Security == proxystore.SecurityReality,
		ServerName: parsed.ServerName, Flow: parsed.Flow, Fingerprint: parsed.Fingerprint,
		RealityPublicKey: parsed.RealityPublicKey, RealityShortID: parsed.RealityShortID,
		Method: parsed.Method, Network: parsed.Network, URI: rewritten,
	}
	if parsed.Protocol == landingstore.ProtocolSOCKS5 {
		node.SOCKSUsername, node.SOCKSPassword = parsed.Username, parsed.Password
	} else if parsed.Protocol == landingstore.ProtocolSS {
		node.ShadowsocksPassword = parsed.Password
	}
	return node, nil
}

func (s *Service) personalAccessibleServerStatus(ctx context.Context, userID, serverID int64) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM servers
		WHERE id = ? AND archived_at IS NULL AND (visibility = 'public' OR EXISTS (
			SELECT 1 FROM server_access WHERE server_access.server_id = servers.id AND server_access.user_id = ?))`,
		serverID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check personal subscription server access: %w", err)
	}
	return status, nil
}

func personalSourceResponse(sourceType string, sourceID int64, defaultName string,
	state personalSourceState,
) PersonalSubscriptionSource {
	return PersonalSubscriptionSource{
		SourceType: sourceType, SourceID: sourceID, Name: state.name, Detail: state.detail,
		DefaultName: defaultName, Status: state.status, StatusDetail: state.statusDetail,
		RequiresClient: state.requiresClient,
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
	return value == PersonalSourceProxy || value == PersonalSourceRelay || value == PersonalSourceLanding || value == PersonalSourceRemoteSource || value == PersonalSourceRemoteNode
}

func normalizePersonalNodeEndpoint(entryHost *string, entryPort *int) (*string, *int, error) {
	if entryHost != nil {
		_, host, err := relaystore.NormalizeEntryHost(relaystore.EntryHostManual, *entryHost)
		if err != nil {
			return nil, nil, ErrInvalidPersonalNodes
		}
		entryHost = &host
	}
	if entryPort != nil {
		if *entryPort < 1 || *entryPort > 65535 {
			return nil, nil, ErrInvalidPersonalNodes
		}
		port := *entryPort
		entryPort = &port
	}
	return entryHost, entryPort, nil
}

func nullablePersonalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func personalSourceKey(sourceType string, sourceID int64) string {
	return fmt.Sprintf("%s:%d", sourceType, sourceID)
}

const personalSubscriptionSelect = `SELECT groups.id, groups.owner_user_id, groups.name,
	groups.subscription_title, groups.token, groups.enabled, groups.client_name,
	groups.routing_preset_id, routing.name, routing.groups_json, groups.routing_bindings_json, groups.shadowrocket_defaults_json,
	groups.mihomo_template_id, COALESCE(templates.name, ''),
	groups.shadowrocket_template_id, COALESCE(shadowrocket_templates.name, ''),
	groups.created_at, groups.updated_at
	FROM personal_subscription_groups AS groups
	JOIN subscription_routing_presets AS routing ON routing.id = groups.routing_preset_id
	LEFT JOIN subscription_templates AS templates ON templates.id = groups.mihomo_template_id
	LEFT JOIN subscription_templates AS shadowrocket_templates ON shadowrocket_templates.id = groups.shadowrocket_template_id `

func scanPersonalSubscription(row rowScanner) (PersonalSubscription, error) {
	var value PersonalSubscription
	var enabled int
	var templateID, shadowrocketTemplateID sql.NullInt64
	var routingGroupsJSON, routingBindingsJSON, defaultsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.OwnerUserID, &value.Name, &value.SubscriptionTitle, &value.Token,
		&enabled, &value.ClientName, &value.RoutingPresetID, &value.RoutingPresetName,
		&routingGroupsJSON, &routingBindingsJSON, &defaultsJSON, &templateID, &value.MihomoTemplateName, &shadowrocketTemplateID, &value.ShadowrocketTemplateName, &createdAt, &updatedAt); err != nil {
		return PersonalSubscription{}, err
	}
	if err := json.Unmarshal([]byte(routingGroupsJSON), &value.routingGroups); err != nil {
		return PersonalSubscription{}, fmt.Errorf("decode personal subscription routing groups: %w", err)
	}
	bindings, err := decodeRoutingBindings(routingBindingsJSON)
	if err != nil {
		return PersonalSubscription{}, err
	}
	value.RoutingBindings = bindings
	value.ShadowrocketDefaults = make(ShadowrocketDefaults)
	if err := json.Unmarshal([]byte(defaultsJSON), &value.ShadowrocketDefaults); err != nil {
		return PersonalSubscription{}, fmt.Errorf("decode Shadowrocket defaults: %w", err)
	}
	if templateID.Valid {
		id := templateID.Int64
		value.MihomoTemplateID = &id
	}
	if shadowrocketTemplateID.Valid {
		id := shadowrocketTemplateID.Int64
		value.ShadowrocketTemplateID = &id
	}
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
