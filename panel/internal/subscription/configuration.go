package subscription

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	rows, err := query.QueryContext(ctx, `SELECT id, name, enabled, is_default, groups_json,
		rule_providers_yaml, rules_json, created_at, updated_at
		FROM subscription_routing_presets ORDER BY is_default DESC, created_at DESC, id DESC`)
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
		`SELECT id, name, enabled, is_default, groups_json, rule_providers_yaml, rules_json, created_at, updated_at
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
	value := RoutingPreset{
		Name: input.Name, Enabled: input.Enabled, Groups: append([]RoutingGroup(nil), input.Groups...),
		RuleProviders: input.RuleProviders, Rules: input.Rules,
	}
	for index := range value.Groups {
		value.Groups[index].Key = ""
	}
	if err := normalizeRoutingPreset(&value); err != nil {
		return RoutingPreset{}, err
	}
	groups, _ := json.Marshal(value.Groups)
	rules, _ := json.Marshal(value.Rules)
	providersYAML, err := marshalRoutingRuleProviders(value.RuleProviders)
	if err != nil {
		return RoutingPreset{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscription_routing_presets
		(name, enabled, is_default, groups_json, rule_providers_yaml, rules_json, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?, ?, ?)`,
		value.Name, value.Enabled, groups, providersYAML, rules, now.Unix(), now.Unix())
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
		if value.IsDefault && !*input.Enabled {
			return RoutingPreset{}, ErrDefaultRoutingPreset
		}
		value.Enabled = *input.Enabled
	}
	if input.Groups != nil {
		existingKeys := make(map[string]struct{}, len(value.Groups))
		for _, group := range value.Groups {
			existingKeys[group.Key] = struct{}{}
		}
		for _, group := range *input.Groups {
			if group.Key != "" {
				if _, exists := existingKeys[group.Key]; !exists {
					return RoutingPreset{}, fmt.Errorf("策略组 Key %q 不能修改: %w", group.Key, ErrRoutingGroupKeyInvalid)
				}
			}
		}
		value.Groups = *input.Groups
	}
	if input.RuleProviders != nil {
		value.RuleProviders = *input.RuleProviders
	}
	if input.Rules != nil {
		value.Rules = *input.Rules
	}
	if err := normalizeRoutingPreset(&value); err != nil {
		return RoutingPreset{}, err
	}
	groups, _ := json.Marshal(value.Groups)
	rules, _ := json.Marshal(value.Rules)
	providersYAML, err := marshalRoutingRuleProviders(value.RuleProviders)
	if err != nil {
		return RoutingPreset{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE subscription_routing_presets
		SET name = ?, enabled = ?, groups_json = ?, rule_providers_yaml = ?, rules_json = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.Enabled, groups, providersYAML, rules,
		s.now().UTC().Truncate(time.Second).Unix(), id); err != nil {
		return RoutingPreset{}, fmt.Errorf("update subscription routing preset: %w", err)
	}
	return s.GetRoutingPreset(ctx, id)
}

func (s *Service) DeleteRoutingPreset(ctx context.Context, id int64) error {
	value, err := s.GetRoutingPreset(ctx, id)
	if err != nil {
		return err
	}
	if value.IsDefault {
		return ErrDefaultRoutingPreset
	}
	var references int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM subscription_plans WHERE routing_preset_id = ?) +
		(SELECT COUNT(*) FROM personal_subscription_groups WHERE routing_preset_id = ?)`, id, id).Scan(&references); err != nil {
		return fmt.Errorf("count subscription routing preset references: %w", err)
	}
	if references != 0 {
		return ErrRoutingPresetReferenced
	}
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

func normalizeRoutingPreset(value *RoutingPreset) error {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes {
		return ErrInvalidRoutingPreset
	}
	providers, providerNames, err := normalizeRoutingRuleProviders(value.RuleProviders)
	if err != nil {
		return err
	}
	value.RuleProviders = providers
	value.Groups, value.Rules, err = normalizeRoutingConfiguration(
		value.Groups, value.Rules, providerNames, ErrInvalidRoutingPreset,
	)
	if err != nil {
		return err
	}
	return validateRoutingConfigurationSize(value.Groups, value.RuleProviders, value.Rules, ErrInvalidRoutingPreset)
}

func normalizeRoutingConfiguration(groups []RoutingGroup, rules []string, providerNames map[string]struct{}, invalid error) ([]RoutingGroup, []string, error) {
	if len(groups) == 0 || len(rules) > maxRoutingRules {
		return nil, nil, invalid
	}
	normalizedGroups := make([]RoutingGroup, 0, len(groups))
	groupsByName := make(map[string]RoutingGroup, len(groups))
	groupKeys := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group.Key = strings.TrimSpace(group.Key)
		if group.Key == "" {
			var err error
			group.Key, err = newRoutingGroupKey(groupKeys)
			if err != nil {
				return nil, nil, fmt.Errorf("生成策略组 Key: %w", err)
			}
		}
		if !validRoutingGroupKey(group.Key) {
			return nil, nil, fmt.Errorf("策略组 Key %q 无效: %w", group.Key, ErrRoutingGroupKeyInvalid)
		}
		if _, exists := groupKeys[group.Key]; exists {
			return nil, nil, fmt.Errorf("策略组 Key %q 重复: %w", group.Key, ErrRoutingGroupKeyInvalid)
		}
		groupKeys[group.Key] = struct{}{}
		group.Name = strings.TrimSpace(group.Name)
		group.Type = strings.TrimSpace(group.Type)
		if group.Name == "" || utf8.RuneCountInString(group.Name) > maxNameRunes || group.Type != "select" ||
			group.Name == "DIRECT" || group.Name == "REJECT" {
			return nil, nil, invalid
		}
		if _, exists := groupsByName[group.Name]; exists {
			return nil, nil, fmt.Errorf("策略组名称 %q 重复: %w", group.Name, ErrRoutingGroupNameDuplicate)
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
		group.Proxies = proxies
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
					return nil, nil, fmt.Errorf("策略组 %q 引用了不存在的策略组 %q: %w",
						group.Name, proxy, ErrRoutingGroupReferenceMissing)
				}
			}
		}
	}
	if routingGroupsCyclic(groupsByName) {
		return nil, nil, fmt.Errorf("策略组存在循环引用: %w", ErrRoutingGroupCycle)
	}
	groupNames := make(map[string]struct{}, len(groupsByName))
	for name := range groupsByName {
		groupNames[name] = struct{}{}
	}
	normalizedRules := make([]string, 0, len(rules))
	for index, rule := range rules {
		rule = strings.TrimSpace(rule)
		policy, provider, err := parseRoutingRule(rule)
		if err != nil {
			return nil, nil, invalid
		}
		if !validRoutingPolicy(policy, groupNames) {
			return nil, nil, fmt.Errorf("第 %d 条 Rule 引用了不存在的策略组 %q: %w",
				index+1, policy, ErrRoutingRuleGroupMissing)
		}
		if provider != "" {
			if _, exists := providerNames[provider]; !exists {
				return nil, nil, fmt.Errorf("第 %d 条 RULE-SET 引用了不存在的 Provider %q: %w",
					index+1, provider, ErrRoutingRuleProviderMissing)
			}
		}
		normalizedRules = append(normalizedRules, rule)
	}
	return normalizedGroups, normalizedRules, nil
}

func parseRoutingRuleProvidersYAML(source string) ([]RoutingRuleProvider, error) {
	source = strings.TrimSpace(source)
	if source == "" || len(source) > maxRoutingConfigurationBytes {
		return nil, ErrInvalidRoutingPreset
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !safeYAMLNode(document.Content[0]) {
		return nil, ErrInvalidRoutingPreset
	}
	root := document.Content[0]
	providers := make([]RoutingRuleProvider, 0, len(root.Content)/2)
	names := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		value := root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || strings.TrimSpace(key.Value) == "" || value.Kind != yaml.MappingNode {
			return nil, ErrInvalidRoutingPreset
		}
		if _, exists := names[key.Value]; exists {
			return nil, ErrInvalidRoutingPreset
		}
		allowed := map[string]bool{"type": true, "behavior": true, "format": true, "interval": true, "url": true}
		seenFields := make(map[string]struct{}, len(value.Content)/2)
		for fieldIndex := 0; fieldIndex < len(value.Content); fieldIndex += 2 {
			field := value.Content[fieldIndex]
			if field.Kind != yaml.ScalarNode || !allowed[field.Value] {
				return nil, fmt.Errorf("unsupported rule provider field %q: %w", field.Value, ErrInvalidRoutingPreset)
			}
			if _, exists := seenFields[field.Value]; exists {
				return nil, ErrInvalidRoutingPreset
			}
			seenFields[field.Value] = struct{}{}
		}
		names[key.Value] = struct{}{}
		var stored struct {
			Type     string `yaml:"type"`
			Behavior string `yaml:"behavior"`
			Format   string `yaml:"format"`
			Interval int    `yaml:"interval"`
			URL      string `yaml:"url"`
		}
		if err := value.Decode(&stored); err != nil {
			return nil, ErrInvalidRoutingPreset
		}
		providers = append(providers, RoutingRuleProvider{
			Name: key.Value, URL: stored.URL, Type: stored.Type, Behavior: stored.Behavior,
			Format: stored.Format, Interval: stored.Interval,
		})
	}
	providers, _, err := normalizeRoutingRuleProviders(providers)
	return providers, err
}

func normalizeRoutingRuleProviders(values []RoutingRuleProvider) ([]RoutingRuleProvider, map[string]struct{}, error) {
	providers := make([]RoutingRuleProvider, 0, len(values))
	names := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Name = strings.TrimSpace(value.Name)
		value.URL = strings.TrimSpace(value.URL)
		value.Type = strings.ToLower(strings.TrimSpace(value.Type))
		value.Behavior = strings.TrimSpace(value.Behavior)
		value.Format = strings.TrimSpace(value.Format)
		parsedURL, err := url.Parse(value.URL)
		if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes || value.Type != "http" ||
			value.Behavior == "" || value.Format == "" || value.Interval <= 0 ||
			strings.ContainsAny(value.Behavior+value.Format, "\r\n") {
			return nil, nil, ErrInvalidRoutingPreset
		}
		if err != nil || parsedURL.Host == "" ||
			(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return nil, nil, fmt.Errorf("Provider %q 的 URL 无效: %w", value.Name, ErrRoutingProviderURLInvalid)
		}
		if _, exists := names[value.Name]; exists {
			return nil, nil, fmt.Errorf("Provider 名称 %q 重复: %w", value.Name, ErrRoutingProviderNameDuplicate)
		}
		names[value.Name] = struct{}{}
		providers = append(providers, value)
	}
	return providers, names, nil
}

func marshalRoutingRuleProviders(values []RoutingRuleProvider) (string, error) {
	values, _, err := normalizeRoutingRuleProviders(values)
	if err != nil {
		return "", err
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, value := range values {
		encoded, err := encodeYAMLValue(struct {
			Type     string `yaml:"type"`
			Behavior string `yaml:"behavior"`
			Format   string `yaml:"format"`
			Interval int    `yaml:"interval"`
			URL      string `yaml:"url"`
		}{value.Type, value.Behavior, value.Format, value.Interval, value.URL})
		if err != nil {
			return "", ErrInvalidRoutingPreset
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value.Name}, encoded)
	}
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return "", ErrInvalidRoutingPreset
	}
	return strings.TrimSpace(string(encoded)), nil
}

func validateRoutingConfigurationSize(groups []RoutingGroup, providers []RoutingRuleProvider, rules []string, invalid error) error {
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		return invalid
	}
	providersYAML, err := marshalRoutingRuleProviders(providers)
	if err != nil {
		return invalid
	}
	rulesJSON, err := json.Marshal(rules)
	if err != nil || len(groupsJSON)+len(providersYAML)+len(rulesJSON) > maxRoutingConfigurationBytes {
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

func newRoutingGroupKey(existing map[string]struct{}) (string, error) {
	for {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		value := "grp_" + hex.EncodeToString(raw[:])
		if _, exists := existing[value]; !exists {
			return value, nil
		}
	}
}

func validRoutingGroupKey(value string) bool {
	if !strings.HasPrefix(value, "grp_") || len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, current := range value[4:] {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
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
	var enabled, isDefault int
	var groupsJSON, providersYAML, rulesJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &enabled, &isDefault, &groupsJSON,
		&providersYAML, &rulesJSON, &createdAt, &updatedAt); err != nil {
		return RoutingPreset{}, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &value.Groups); err != nil {
		return RoutingPreset{}, fmt.Errorf("decode routing groups: %w", err)
	}
	if err := json.Unmarshal([]byte(rulesJSON), &value.Rules); err != nil {
		return RoutingPreset{}, fmt.Errorf("decode routing rules: %w", err)
	}
	providers, err := parseRoutingRuleProvidersYAML(providersYAML)
	if err != nil {
		return RoutingPreset{}, fmt.Errorf("decode routing rule providers: %w", err)
	}
	value.RuleProviders = providers
	value.Enabled = enabled != 0
	value.IsDefault = isDefault != 0
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
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM subscription_plans WHERE template_id = ?) +
		(SELECT COUNT(*) FROM personal_subscription_groups WHERE mihomo_template_id = ?)`, id, id).Scan(&references); err != nil {
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
	seen := make(map[string]struct{}, len(document.Content[0].Content)/2)
	for index := 0; index < len(document.Content[0].Content); index += 2 {
		key := document.Content[0].Content[index].Value
		if _, exists := seen[key]; exists {
			return ErrInvalidTemplate
		}
		seen[key] = struct{}{}
		switch key {
		case "proxies", "proxy-groups", "rule-providers", "rules":
			return ErrInvalidTemplate
		}
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

func resolvePlanRoutingPresetRef(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, routingPresetID, allowedDisabledID *int64) (*int64, error) {
	if routingPresetID == nil {
		if allowedDisabledID != nil {
			return nil, ErrRoutingPresetNotFound
		}
		var id int64
		if err := query.QueryRowContext(ctx,
			`SELECT id FROM subscription_routing_presets WHERE is_default = 1 AND enabled = 1`).Scan(&id); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRoutingPresetNotFound
		} else if err != nil {
			return nil, fmt.Errorf("find default subscription routing preset: %w", err)
		}
		return &id, nil
	}
	if *routingPresetID <= 0 {
		return nil, ErrRoutingPresetNotFound
	}
	var enabled int
	if err := query.QueryRowContext(ctx,
		`SELECT enabled FROM subscription_routing_presets WHERE id = ?`, *routingPresetID).Scan(&enabled); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRoutingPresetNotFound
	} else if err != nil {
		return nil, fmt.Errorf("validate subscription plan routing preset: %w", err)
	}
	if enabled == 0 && (allowedDisabledID == nil || *allowedDisabledID != *routingPresetID) {
		return nil, ErrInvalidPlanRouting
	}
	id := *routingPresetID
	return &id, nil
}
