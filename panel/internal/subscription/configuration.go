package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	maxSubscriptionTemplateBytes = 64 << 10
	maxRoutingConfigurationBytes = 64 << 10
	maxRoutingRules              = 512
	maxRoutingRuleRunes          = 1024
)

func (s *Service) ListRoutingPresets(ctx context.Context) ([]RoutingPreset, error) {
	return listRoutingPresets(ctx, s.db)
}

func listRoutingPresets(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]RoutingPreset, error) {
	rows, err := query.QueryContext(ctx, `SELECT id, name, enabled, groups_json, rules_json, created_at, updated_at
		FROM subscription_routing_presets ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list subscription routing presets: %w", err)
	}
	defer rows.Close()
	values := make([]RoutingPreset, 0)
	for rows.Next() {
		value, err := scanRoutingPreset(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription routing preset: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) GetRoutingPreset(ctx context.Context, id int64) (RoutingPreset, error) {
	value, err := scanRoutingPreset(s.db.QueryRowContext(ctx,
		`SELECT id, name, enabled, groups_json, rules_json, created_at, updated_at
		 FROM subscription_routing_presets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RoutingPreset{}, ErrRoutingPresetNotFound
	}
	if err != nil {
		return RoutingPreset{}, fmt.Errorf("get subscription routing preset: %w", err)
	}
	return value, nil
}

func (s *Service) CreateRoutingPreset(ctx context.Context, input CreateRoutingPresetInput) (RoutingPreset, error) {
	value := RoutingPreset{Name: input.Name, Enabled: input.Enabled, Groups: input.Groups, Rules: input.Rules}
	value.Name = strings.TrimSpace(value.Name)
	var err error
	value.Groups, value.Rules, err = normalizeRoutingConfiguration(value.Groups, value.Rules, false, ErrInvalidRoutingPreset)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes || err != nil {
		if err != nil {
			return RoutingPreset{}, err
		}
		return RoutingPreset{}, ErrInvalidRoutingPreset
	}
	if err := validateRoutingJSONSize(value.Groups, value.Rules, ErrInvalidRoutingPreset); err != nil {
		return RoutingPreset{}, err
	}
	groups, _ := json.Marshal(value.Groups)
	rules, _ := json.Marshal(value.Rules)
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscription_routing_presets
		(name, enabled, groups_json, rules_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		value.Name, value.Enabled, groups, rules, now.Unix(), now.Unix())
	if err != nil {
		return RoutingPreset{}, fmt.Errorf("create subscription routing preset: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoutingPreset{}, fmt.Errorf("read subscription routing preset id: %w", err)
	}
	return s.GetRoutingPreset(ctx, id)
}

func (s *Service) UpdateRoutingPreset(ctx context.Context, id int64, input UpdateRoutingPresetInput) (RoutingPreset, error) {
	value, err := s.GetRoutingPreset(ctx, id)
	if err != nil {
		return RoutingPreset{}, err
	}
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	if input.Groups != nil {
		value.Groups = *input.Groups
	}
	if input.Rules != nil {
		value.Rules = *input.Rules
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Groups, value.Rules, err = normalizeRoutingConfiguration(value.Groups, value.Rules, false, ErrInvalidRoutingPreset)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes || err != nil {
		if err != nil {
			return RoutingPreset{}, err
		}
		return RoutingPreset{}, ErrInvalidRoutingPreset
	}
	if err := validateRoutingJSONSize(value.Groups, value.Rules, ErrInvalidRoutingPreset); err != nil {
		return RoutingPreset{}, err
	}
	groups, _ := json.Marshal(value.Groups)
	rules, _ := json.Marshal(value.Rules)
	if _, err := s.db.ExecContext(ctx, `UPDATE subscription_routing_presets
		SET name = ?, enabled = ?, groups_json = ?, rules_json = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.Enabled, groups, rules, s.now().UTC().Truncate(time.Second).Unix(), id); err != nil {
		return RoutingPreset{}, fmt.Errorf("update subscription routing preset: %w", err)
	}
	return s.GetRoutingPreset(ctx, id)
}

func (s *Service) DeleteRoutingPreset(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM subscription_routing_presets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete subscription routing preset: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read subscription routing preset deletion: %w", err)
	} else if count != 1 {
		return ErrRoutingPresetNotFound
	}
	return nil
}

func normalizeRoutingConfiguration(groups []RoutingGroup, rules []string, allowEmpty bool, invalid error) ([]RoutingGroup, []string, error) {
	if len(groups) == 0 && len(rules) == 0 {
		if allowEmpty {
			return []RoutingGroup{}, []string{}, nil
		}
		return nil, nil, invalid
	}
	if len(groups) == 0 || len(rules) > maxRoutingRules {
		return nil, nil, invalid
	}
	normalizedGroups := make([]RoutingGroup, 0, len(groups))
	groupsByName := make(map[string]RoutingGroup, len(groups))
	for _, group := range groups {
		group.Name = strings.TrimSpace(group.Name)
		group.Type = strings.TrimSpace(group.Type)
		if group.Name == "" || utf8.RuneCountInString(group.Name) > maxNameRunes || group.Type != "select" ||
			group.Name == "DIRECT" || group.Name == "REJECT" {
			return nil, nil, invalid
		}
		if _, exists := groupsByName[group.Name]; exists {
			return nil, nil, invalid
		}
		proxies := make([]string, 0, len(group.Proxies))
		seenProxies := make(map[string]struct{}, len(group.Proxies))
		for _, proxy := range group.Proxies {
			proxy = strings.TrimSpace(proxy)
			if proxy == "" {
				return nil, nil, invalid
			}
			if _, exists := seenProxies[proxy]; !exists {
				proxies = append(proxies, proxy)
				seenProxies[proxy] = struct{}{}
			}
		}
		nodeIDs := make([]int64, 0, len(group.NodeIDs))
		seenNodeIDs := make(map[int64]struct{}, len(group.NodeIDs))
		for _, nodeID := range group.NodeIDs {
			if nodeID <= 0 {
				return nil, nil, invalid
			}
			if _, exists := seenNodeIDs[nodeID]; !exists {
				nodeIDs = append(nodeIDs, nodeID)
				seenNodeIDs[nodeID] = struct{}{}
			}
		}
		group.Proxies = proxies
		group.NodeIDs = nodeIDs
		normalizedGroups = append(normalizedGroups, group)
		groupsByName[group.Name] = group
	}
	for _, group := range normalizedGroups {
		for _, proxy := range group.Proxies {
			if proxy == group.Name {
				return nil, nil, invalid
			}
			if proxy != "DIRECT" && proxy != "REJECT" {
				if _, exists := groupsByName[proxy]; !exists {
					return nil, nil, invalid
				}
			}
		}
	}
	if routingGroupsCyclic(groupsByName) {
		return nil, nil, invalid
	}
	groupNames := make(map[string]struct{}, len(groupsByName))
	for name := range groupsByName {
		groupNames[name] = struct{}{}
	}
	normalizedRules := make([]string, 0, len(rules))
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		policy, _, err := parseRoutingRule(rule)
		if err != nil || !validRoutingPolicy(policy, groupNames) {
			return nil, nil, invalid
		}
		normalizedRules = append(normalizedRules, rule)
	}
	return normalizedGroups, normalizedRules, nil
}

func validateRoutingJSONSize(groups []RoutingGroup, rules []string, invalid error) error {
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		return invalid
	}
	rulesJSON, err := json.Marshal(rules)
	if err != nil || len(groupsJSON)+len(rulesJSON) > maxRoutingConfigurationBytes {
		return invalid
	}
	return nil
}

func parseRoutingRule(rule string) (string, string, error) {
	if rule == "" || strings.ContainsAny(rule, "\r\n") || utf8.RuneCountInString(rule) > maxRoutingRuleRunes {
		return "", "", ErrInvalidPlanRouting
	}
	parts := strings.Split(rule, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return "", "", ErrInvalidPlanRouting
		}
	}
	switch parts[0] {
	case "MATCH":
		if len(parts) != 2 {
			return "", "", ErrInvalidPlanRouting
		}
		return parts[1], "", nil
	case "RULE-SET":
		if len(parts) != 3 && (len(parts) != 4 || parts[3] != "no-resolve") {
			return "", "", ErrInvalidPlanRouting
		}
		return parts[2], parts[1], nil
	default:
		if len(parts) < 3 {
			return "", "", ErrInvalidPlanRouting
		}
		policyIndex := len(parts) - 1
		if parts[policyIndex] == "no-resolve" {
			if len(parts) < 4 {
				return "", "", ErrInvalidPlanRouting
			}
			policyIndex--
		}
		return parts[policyIndex], "", nil
	}
}

func validRoutingPolicy(policy string, groupNames map[string]struct{}) bool {
	if policy == "DIRECT" || policy == "REJECT" {
		return true
	}
	_, exists := groupNames[policy]
	return exists
}

func routingGroupsCyclic(groups map[string]RoutingGroup) bool {
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(name string) bool {
		if visiting[name] {
			return true
		}
		if visited[name] {
			return false
		}
		visiting[name] = true
		for _, member := range groups[name].Proxies {
			if _, isGroup := groups[member]; isGroup && visit(member) {
				return true
			}
		}
		visiting[name] = false
		visited[name] = true
		return false
	}
	for name := range groups {
		if visit(name) {
			return true
		}
	}
	return false
}

func scanRoutingPreset(row rowScanner) (RoutingPreset, error) {
	var value RoutingPreset
	var enabled int
	var groupsJSON, rulesJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &enabled, &groupsJSON, &rulesJSON, &createdAt, &updatedAt); err != nil {
		return RoutingPreset{}, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &value.Groups); err != nil {
		return RoutingPreset{}, fmt.Errorf("decode routing groups: %w", err)
	}
	if err := json.Unmarshal([]byte(rulesJSON), &value.Rules); err != nil {
		return RoutingPreset{}, fmt.Errorf("decode routing rules: %w", err)
	}
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (s *Service) ListTemplates(ctx context.Context) ([]SubscriptionTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, enabled, config_yaml, created_at, updated_at
		FROM subscription_templates ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list subscription templates: %w", err)
	}
	defer rows.Close()
	values := make([]SubscriptionTemplate, 0)
	for rows.Next() {
		value, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription template: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) GetTemplate(ctx context.Context, id int64) (SubscriptionTemplate, error) {
	value, err := scanTemplate(s.db.QueryRowContext(ctx,
		`SELECT id, name, enabled, config_yaml, created_at, updated_at FROM subscription_templates WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionTemplate{}, ErrTemplateNotFound
	}
	if err != nil {
		return SubscriptionTemplate{}, fmt.Errorf("get subscription template: %w", err)
	}
	return value, nil
}

func (s *Service) CreateTemplate(ctx context.Context, input CreateSubscriptionTemplateInput) (SubscriptionTemplate, error) {
	value := SubscriptionTemplate{Name: input.Name, Enabled: input.Enabled, ConfigYAML: input.ConfigYAML}
	if err := validateTemplate(&value); err != nil {
		return SubscriptionTemplate{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscription_templates
		(name, enabled, config_yaml, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		value.Name, value.Enabled, value.ConfigYAML, now.Unix(), now.Unix())
	if err != nil {
		return SubscriptionTemplate{}, fmt.Errorf("create subscription template: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SubscriptionTemplate{}, fmt.Errorf("read subscription template id: %w", err)
	}
	return s.GetTemplate(ctx, id)
}

func (s *Service) UpdateTemplate(ctx context.Context, id int64, input UpdateSubscriptionTemplateInput) (SubscriptionTemplate, error) {
	value, err := s.GetTemplate(ctx, id)
	if err != nil {
		return SubscriptionTemplate{}, err
	}
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	if input.ConfigYAML != nil {
		value.ConfigYAML = *input.ConfigYAML
	}
	if err := validateTemplate(&value); err != nil {
		return SubscriptionTemplate{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE subscription_templates
		SET name = ?, enabled = ?, config_yaml = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.Enabled, value.ConfigYAML, s.now().UTC().Truncate(time.Second).Unix(), id); err != nil {
		return SubscriptionTemplate{}, fmt.Errorf("update subscription template: %w", err)
	}
	return s.GetTemplate(ctx, id)
}

func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	var references int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_plans WHERE template_id = ?`, id).Scan(&references); err != nil {
		return fmt.Errorf("count subscription template references: %w", err)
	}
	if references != 0 {
		return ErrTemplateReferenced
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM subscription_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete subscription template: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read subscription template deletion: %w", err)
	} else if count != 1 {
		return ErrTemplateNotFound
	}
	return nil
}

func validateTemplate(value *SubscriptionTemplate) error {
	value.Name = strings.TrimSpace(value.Name)
	value.ConfigYAML = strings.TrimSpace(value.ConfigYAML)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes ||
		value.ConfigYAML == "" || len(value.ConfigYAML) > maxSubscriptionTemplateBytes {
		return ErrInvalidTemplate
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(value.ConfigYAML), &document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !safeYAMLNode(document.Content[0]) {
		return ErrInvalidTemplate
	}
	hasGroups, hasRules := false, false
	seen := make(map[string]struct{}, len(document.Content[0].Content)/2)
	for index := 0; index < len(document.Content[0].Content); index += 2 {
		key := document.Content[0].Content[index].Value
		if _, exists := seen[key]; exists {
			return ErrInvalidTemplate
		}
		seen[key] = struct{}{}
		switch key {
		case "proxies":
			return ErrInvalidTemplate
		case "proxy-groups":
			hasGroups = true
			if document.Content[0].Content[index+1].Kind != yaml.SequenceNode {
				return ErrInvalidTemplate
			}
		case "rules":
			hasRules = true
			if document.Content[0].Content[index+1].Kind != yaml.SequenceNode {
				return ErrInvalidTemplate
			}
		case "rule-providers":
			if document.Content[0].Content[index+1].Kind != yaml.MappingNode {
				return ErrInvalidTemplate
			}
		}
	}
	if hasGroups != hasRules {
		return ErrInvalidTemplate
	}
	return nil
}

func safeYAMLNode(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	allowedTags := map[string]bool{
		"": true, "!!map": true, "!!seq": true, "!!str": true, "!!bool": true,
		"!!int": true, "!!float": true, "!!null": true, "tag:yaml.org,2002:map": true,
		"tag:yaml.org,2002:seq": true, "tag:yaml.org,2002:str": true,
		"tag:yaml.org,2002:bool": true, "tag:yaml.org,2002:int": true,
		"tag:yaml.org,2002:float": true, "tag:yaml.org,2002:null": true,
	}
	if !allowedTags[node.Tag] {
		return false
	}
	for _, child := range node.Content {
		if !safeYAMLNode(child) {
			return false
		}
	}
	return true
}

func scanTemplate(row rowScanner) (SubscriptionTemplate, error) {
	var value SubscriptionTemplate
	var enabled int
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &enabled, &value.ConfigYAML, &createdAt, &updatedAt); err != nil {
		return SubscriptionTemplate{}, err
	}
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func validatePlanTemplateRef(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, templateID *int64) error {
	if templateID == nil {
		return nil
	}
	if *templateID <= 0 {
		return ErrTemplateNotFound
	}
	var exists int
	if err := query.QueryRowContext(ctx, `SELECT 1 FROM subscription_templates WHERE id = ?`, *templateID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrTemplateNotFound
	} else if err != nil {
		return fmt.Errorf("validate subscription plan template: %w", err)
	}
	return nil
}
