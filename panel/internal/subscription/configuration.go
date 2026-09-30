package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxSubscriptionTemplateBytes = 64 << 10

var routingIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

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
	if err := s.validateRoutingPreset(ctx, value); err != nil {
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
	if err := s.validateRoutingPreset(ctx, value); err != nil {
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
	var references int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_plans WHERE routing_preset_id = ?`, id).Scan(&references); err != nil {
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

func (s *Service) validateRoutingPreset(ctx context.Context, value RoutingPreset) error {
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes || len(value.Groups) == 0 {
		return ErrInvalidRoutingPreset
	}
	groups := make(map[string]RoutingGroup, len(value.Groups))
	for _, group := range value.Groups {
		if !routingIDPattern.MatchString(group.ID) || strings.TrimSpace(group.Name) == "" ||
			utf8.RuneCountInString(group.Name) > maxNameRunes || group.Type != "select" {
			return ErrInvalidRoutingPreset
		}
		if _, exists := groups[group.ID]; exists {
			return ErrInvalidRoutingPreset
		}
		groups[group.ID] = group
	}
	for _, group := range value.Groups {
		if len(group.Members) == 0 {
			return ErrInvalidRoutingPreset
		}
		for _, member := range group.Members {
			switch member.Type {
			case "published_node":
				if member.PublishedNodeID <= 0 || member.GroupID != "" {
					return ErrInvalidRoutingPreset
				}
				var exists int
				if err := s.db.QueryRowContext(ctx,
					`SELECT 1 FROM subscription_published_nodes WHERE id = ?`, member.PublishedNodeID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
					return ErrPublishedNodeNotFound
				} else if err != nil {
					return fmt.Errorf("validate routing preset published node: %w", err)
				}
			case "direct":
				if member.PublishedNodeID != 0 || member.GroupID != "" {
					return ErrInvalidRoutingPreset
				}
			case "group":
				if member.PublishedNodeID != 0 || member.GroupID == group.ID {
					return ErrInvalidRoutingPreset
				}
				if _, exists := groups[member.GroupID]; !exists {
					return ErrInvalidRoutingPreset
				}
			default:
				return ErrInvalidRoutingPreset
			}
		}
	}
	if routingGroupsCyclic(groups) {
		return ErrInvalidRoutingPreset
	}
	for _, rule := range value.Rules {
		switch rule.Type {
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "GEOIP", "GEOSITE":
			if strings.TrimSpace(rule.Value) == "" || strings.ContainsAny(rule.Value, "\r\n,") {
				return ErrInvalidRoutingPreset
			}
		case "MATCH":
			if strings.TrimSpace(rule.Value) != "" {
				return ErrInvalidRoutingPreset
			}
		default:
			return ErrInvalidRoutingPreset
		}
		if _, exists := groups[rule.TargetGroupID]; !exists {
			return ErrInvalidRoutingPreset
		}
	}
	return nil
}

func routingGroupsCyclic(groups map[string]RoutingGroup) bool {
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, member := range groups[id].Members {
			if member.Type == "group" && visit(member.GroupID) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range groups {
		if visit(id) {
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
	for index := 0; index < len(document.Content[0].Content); index += 2 {
		switch document.Content[0].Content[index].Value {
		case "proxies", "proxy-groups", "rules":
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

func validatePlanConfigurationRefs(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, routingPresetID, templateID *int64) error {
	for _, item := range []struct {
		id       *int64
		table    string
		notFound error
	}{
		{routingPresetID, "subscription_routing_presets", ErrRoutingPresetNotFound},
		{templateID, "subscription_templates", ErrTemplateNotFound},
	} {
		if item.id == nil {
			continue
		}
		if *item.id <= 0 {
			return item.notFound
		}
		var exists int
		if err := query.QueryRowContext(ctx, `SELECT 1 FROM `+item.table+` WHERE id = ?`, *item.id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return item.notFound
		} else if err != nil {
			return fmt.Errorf("validate subscription plan configuration: %w", err)
		}
	}
	return nil
}

func routingPresetReferencesNode(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, nodeID int64) (bool, error) {
	values, err := listRoutingPresets(ctx, query)
	if err != nil {
		return false, err
	}
	for _, value := range values {
		for _, group := range value.Groups {
			for _, member := range group.Members {
				if member.Type == "published_node" && member.PublishedNodeID == nodeID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
