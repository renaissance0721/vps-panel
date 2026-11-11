package subscription

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

var ErrRoutingINIConflict = errors.New("routing preset revision conflict")
var ErrRoutingINIBindings = errors.New("removed routing groups still have subscription bindings")
var ErrRoutingINIInvalid = errors.New("invalid routing INI")

type RoutingINIChanges struct {
	GroupsAdded          []string          `json:"groups_added"`
	GroupsRemoved        []string          `json:"groups_removed"`
	GroupsChanged        []string          `json:"groups_changed"`
	ProvidersAdded       []string          `json:"providers_added"`
	ProvidersRemoved     []string          `json:"providers_removed"`
	ProvidersChanged     []string          `json:"providers_changed"`
	RulesBefore          []string          `json:"rules_before"`
	RulesAfter           []string          `json:"rules_after"`
	RulesAdded           []string          `json:"rules_added"`
	RulesRemoved         []string          `json:"rules_removed"`
	RetainedGroupKeys    map[string]string `json:"retained_group_keys"`
	RemovedGroupKeys     map[string]string `json:"removed_group_keys"`
	InvalidBindingGroups []string          `json:"invalid_binding_groups"`
}
type RoutingINIPreview struct {
	Result   RoutingINIResult  `json:"result"`
	Revision string            `json:"revision,omitempty"`
	Changes  RoutingINIChanges `json:"changes"`
}

func RoutingINIRevision(p RoutingPreset) string {
	value := struct {
		ID        int64
		Name      string
		Enabled   bool
		Groups    []RoutingGroup
		Providers []RoutingRuleProvider
		Rules     []string
	}{p.ID, p.Name, p.Enabled, p.Groups, p.RuleProviders, p.Rules}
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func routingINIChanges(previous *RoutingPreset, parsed *RoutingINIResult) RoutingINIChanges {
	changes := RoutingINIChanges{GroupsAdded: []string{}, GroupsRemoved: []string{}, GroupsChanged: []string{}, ProvidersAdded: []string{}, ProvidersRemoved: []string{}, ProvidersChanged: []string{}, RulesBefore: []string{}, RulesAfter: append([]string{}, parsed.Rules...), RulesAdded: []string{}, RulesRemoved: []string{}, RetainedGroupKeys: map[string]string{}, RemovedGroupKeys: map[string]string{}, InvalidBindingGroups: []string{}}
	oldGroups := map[string]RoutingGroup{}
	oldProviders := map[string]RoutingRuleProvider{}
	if previous != nil {
		for _, v := range previous.Groups {
			oldGroups[v.Name] = v
		}
		for _, v := range previous.RuleProviders {
			oldProviders[v.Name] = v
		}
		changes.RulesBefore = append(changes.RulesBefore, previous.Rules...)
	}
	newGroups := map[string]bool{}
	for i := range parsed.Groups {
		v := &parsed.Groups[i]
		// Unlike an omitted field in older visual clients, an INI with no regex
		// explicitly clears the old expressions.
		if v.NodePatterns == nil {
			v.NodePatterns = []string{}
		}
		newGroups[v.Name] = true
		if old, ok := oldGroups[v.Name]; ok {
			v.Key = old.Key
			changes.RetainedGroupKeys[v.Name] = old.Key
			if old.Key != v.Key || old.Name != v.Name || old.Type != v.Type || old.IncludeAll != v.IncludeAll || !slices.Equal(old.Proxies, v.Proxies) || !slices.Equal(old.NodePatterns, v.NodePatterns) {
				changes.GroupsChanged = append(changes.GroupsChanged, v.Name)
			}
		} else {
			changes.GroupsAdded = append(changes.GroupsAdded, v.Name)
		}
	}
	if previous != nil {
		for _, v := range previous.Groups {
			if !newGroups[v.Name] {
				changes.GroupsRemoved = append(changes.GroupsRemoved, v.Name)
				changes.RemovedGroupKeys[v.Name] = v.Key
			}
		}
	}
	newProviders := map[string]bool{}
	for _, v := range parsed.RuleProviders {
		newProviders[v.Name] = true
		if old, ok := oldProviders[v.Name]; !ok {
			changes.ProvidersAdded = append(changes.ProvidersAdded, v.Name)
		} else if !reflect.DeepEqual(old, v) {
			changes.ProvidersChanged = append(changes.ProvidersChanged, v.Name)
		}
	}
	if previous != nil {
		for _, v := range previous.RuleProviders {
			if !newProviders[v.Name] {
				changes.ProvidersRemoved = append(changes.ProvidersRemoved, v.Name)
			}
		}
	}
	changes.RulesAdded = routingINIRuleDifference(changes.RulesAfter, changes.RulesBefore)
	changes.RulesRemoved = routingINIRuleDifference(changes.RulesBefore, changes.RulesAfter)
	return changes
}

func routingINIRuleDifference(left, right []string) []string {
	counts := map[string]int{}
	for _, v := range right {
		counts[v]++
	}
	out := []string{}
	for _, v := range left {
		if counts[v] > 0 {
			counts[v]--
		} else {
			out = append(out, v)
		}
	}
	return out
}

func routingINIBindingConflicts(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, previous RoutingPreset, groups []RoutingGroup) ([]string, error) {
	keys := map[string]bool{}
	for _, v := range groups {
		keys[v.Key] = true
	}
	removed := map[string]string{}
	for _, v := range previous.Groups {
		if !keys[v.Key] {
			removed[v.Key] = v.Name
		}
	}
	if len(removed) == 0 {
		return []string{}, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT routing_bindings_json FROM subscription_plans WHERE routing_preset_id = ? UNION ALL SELECT routing_bindings_json FROM personal_subscription_groups WHERE routing_preset_id = ?`, previous.ID, previous.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		bindings, err := decodeRoutingBindings(raw)
		if err != nil {
			return nil, err
		}
		for key, ids := range bindings {
			if len(ids) > 0 && removed[key] != "" {
				seen[key] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range previous.Groups {
		if seen[v.Key] {
			out = append(out, v.Name)
		}
	}
	return out, nil
}

func (s *Service) PreviewRoutingINI(ctx context.Context, source string, id *int64) (RoutingINIPreview, error) {
	out := RoutingINIPreview{Result: ParseRoutingINI(source)}
	var previous *RoutingPreset
	if id != nil {
		p, err := s.GetRoutingPreset(ctx, *id)
		if err != nil {
			return out, err
		}
		previous = &p
		out.Revision = RoutingINIRevision(p)
	}
	out.Changes = routingINIChanges(previous, &out.Result)
	if previous != nil && len(out.Result.Diagnostics) == 0 {
		conflicts, err := routingINIBindingConflicts(ctx, s.db, *previous, out.Result.Groups)
		if err != nil {
			return out, err
		}
		out.Changes.InvalidBindingGroups = conflicts
	}
	return out, nil
}

func (s *Service) CreateRoutingFromINI(ctx context.Context, name string, enabled bool, source string) (RoutingPreset, RoutingINIPreview, error) {
	p, err := s.PreviewRoutingINI(ctx, source, nil)
	if err != nil {
		return RoutingPreset{}, p, err
	}
	if len(p.Result.Diagnostics) > 0 {
		return RoutingPreset{}, p, ErrRoutingINIInvalid
	}
	v, err := s.CreateRoutingPreset(ctx, CreateRoutingPresetInput{Name: name, Enabled: enabled, Groups: p.Result.Groups, RuleProviders: p.Result.RuleProviders, Rules: p.Result.Rules})
	return v, p, err
}

func (s *Service) UpdateRoutingFromINI(ctx context.Context, id int64, revision, source string) (RoutingPreset, RoutingINIPreview, error) {
	out := RoutingINIPreview{Result: ParseRoutingINI(source)}
	if len(out.Result.Diagnostics) > 0 {
		return RoutingPreset{}, out, ErrRoutingINIInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RoutingPreset{}, out, err
	}
	defer tx.Rollback()
	previous, err := scanRoutingPreset(tx.QueryRowContext(ctx, `SELECT id, name, enabled, is_default, groups_json, rule_providers_yaml, rules_json, created_at, updated_at FROM subscription_routing_presets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RoutingPreset{}, out, ErrRoutingPresetNotFound
	}
	if err != nil {
		return RoutingPreset{}, out, err
	}
	out.Revision = RoutingINIRevision(previous)
	if revision == "" || revision != out.Revision {
		return RoutingPreset{}, out, ErrRoutingINIConflict
	}
	out.Changes = routingINIChanges(&previous, &out.Result)
	conflicts, err := routingINIBindingConflicts(ctx, tx, previous, out.Result.Groups)
	if err != nil {
		return RoutingPreset{}, out, err
	}
	out.Changes.InvalidBindingGroups = conflicts
	if len(conflicts) > 0 {
		return RoutingPreset{}, out, ErrRoutingINIBindings
	}
	updated, err := prepareRoutingPresetUpdate(previous, UpdateRoutingPresetInput{Groups: &out.Result.Groups, RuleProviders: &out.Result.RuleProviders, Rules: &out.Result.Rules})
	if err != nil {
		return RoutingPreset{}, out, err
	}
	if RoutingINIRevision(updated) == out.Revision {
		if err := tx.Commit(); err != nil {
			return RoutingPreset{}, out, err
		}
		return previous, out, nil
	}
	groups, _ := json.Marshal(updated.Groups)
	rules, _ := json.Marshal(updated.Rules)
	providers, err := marshalRoutingRuleProviders(updated.RuleProviders)
	if err != nil {
		return RoutingPreset{}, out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE subscription_routing_presets SET groups_json=?, rule_providers_yaml=?, rules_json=?, updated_at=? WHERE id=?`, groups, providers, rules, s.now().UTC().Truncate(time.Second).Unix(), id)
	if err != nil {
		return RoutingPreset{}, out, fmt.Errorf("update routing INI: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RoutingPreset{}, out, err
	}
	updated, err = s.GetRoutingPreset(ctx, id)
	return updated, out, err
}
