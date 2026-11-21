package subscription

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
)

var (
	ErrInvalidShadowrocketDefault  = errors.New("Shadowrocket 默认策略必须是当前策略组的可选成员")
	ErrShadowrocketDefaultsChanged = errors.New("Shadowrocket 默认策略已被更新，请重新载入订阅后保存")
)

// Use personal node IDs, preset group keys, and reserved policy names as identities.
type ShadowrocketPolicyTarget struct {
	Type     string `json:"type"`
	NodeID   int64  `json:"node_id,omitempty"`
	GroupKey string `json:"group_key,omitempty"`
	Policy   string `json:"policy,omitempty"`
}

type ShadowrocketDefaults map[string]ShadowrocketPolicyTarget
type ShadowrocketDefaultUpdates map[string]*ShadowrocketPolicyTarget

type ShadowrocketPolicyOption struct {
	Target ShadowrocketPolicyTarget `json:"target"`
	Name   string                   `json:"name"`
}

type shadowrocketPolicies struct {
	nodes     []ResolvedSubscriptionNode
	groups    []resolvedRoutingGroup
	projected string
	selected  map[string]string
	options   map[string][]ShadowrocketPolicyOption
}

func resolveShadowrocketPolicies(data PersonalSubscriptionData, routing RoutingPreset, bindingOrder routingBindingOrder, preview bool) (shadowrocketPolicies, error) {
	result := shadowrocketPolicies{}
	names, bindings := expandedPersonalBindings(data)
	result.projected = shadowrocketDefaultProxyGroup(routing, bindings)
	reserved := map[string]bool{"DIRECT": true, "REJECT": true, shadowrocketNativeProxy: true}
	for _, group := range routing.Groups {
		if group.Name == shadowrocketNativeProxy && group.Name != result.projected {
			return result, ErrInvalidRoutingPreset
		}
		reserved[group.Name] = true
	}
	matchNames := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		matchNames = append(matchNames, node.Name)
	}
	nodes, names, err := shadowrocketNodeNames(data.Nodes, names, reserved)
	if err != nil {
		return result, err
	}
	result.nodes = nodes
	allNames := make([]string, 0, len(nodes))
	nodesByName := make(map[string]ResolvedSubscriptionNode, len(nodes))
	for _, node := range nodes {
		allNames = append(allNames, node.Name)
		nodesByName[node.Name] = node
	}
	result.groups, err = resolveRoutingGroupMembers(routing.Groups, bindings, names, allNames, matchNames, bindingOrder, preview)
	if err != nil {
		return result, err
	}
	for _, group := range result.groups {
		if group.Name == result.projected {
			continue
		}
		for _, member := range group.Proxies {
			if !reserved[member] {
				if _, exists := nodesByName[member]; !exists {
					return result, ErrInvalidRoutingBindings
				}
			}
		}
	}
	if preview || len(data.ShadowrocketDefaults) > 0 {
		result.resolveDefaults(routing, data, names, nodesByName, preview)
	}
	if result.projected != "" {
		for _, group := range result.groups {
			for index, member := range group.Proxies {
				if member == result.projected {
					group.Proxies[index] = shadowrocketNativeProxy
				}
			}
		}
	}
	return result, nil
}

func (result *shadowrocketPolicies) resolveDefaults(routing RoutingPreset, data PersonalSubscriptionData,
	names map[int64]string, nodesByName map[string]ResolvedSubscriptionNode, preview bool,
) {
	targets := map[string]ShadowrocketPolicyTarget{
		"DIRECT": {Type: "builtin", Policy: "DIRECT"},
		"REJECT": {Type: "builtin", Policy: "REJECT"},
	}
	for index, group := range routing.Groups {
		if len(result.groups[index].Proxies) > 0 {
			targets[group.Name] = ShadowrocketPolicyTarget{Type: "group", GroupKey: group.Key}
		}
	}
	// Only individually selected nodes have persistent per-subscription identities.
	// expandedPersonalBindings also assigns temporary IDs to whole-source members.
	for id := range data.NodeNames {
		name := names[id]
		node, exists := nodesByName[name]
		if !exists || (id <= 0 && !preview) {
			continue
		}
		if preview {
			if _, err := renderShadowrocketProxy(node); err != nil {
				continue
			}
		}
		targets[name] = ShadowrocketPolicyTarget{Type: "node", NodeID: id}
	}
	result.selected = make(map[string]string)
	if preview {
		result.options = make(map[string][]ShadowrocketPolicyOption)
	}
	for index, group := range result.groups {
		key := routing.Groups[index].Key
		if group.Name == shadowrocketNativeProxy {
			// A declaration named PROXY would collide with the built-in policy.
			continue
		}
		saved, hasDefault := data.ShadowrocketDefaults[key]
		if !preview && !hasDefault {
			continue
		}
		for _, name := range group.Proxies {
			target, exists := targets[name]
			if !exists || !safeShadowrocketValue(name) {
				continue
			}
			if preview {
				result.options[key] = append(result.options[key], ShadowrocketPolicyOption{Target: target, Name: name})
			}
			if hasDefault && saved == target {
				result.selected[key] = name
				if group.Name == result.projected {
					result.projected = ""
				}
			}
		}
	}
	if result.projected != "" {
		for key, name := range result.selected {
			if name == result.projected {
				result.selected[key] = shadowrocketNativeProxy
			}
		}
		for _, options := range result.options {
			for index := range options {
				if options[index].Name == result.projected {
					options[index].Name = shadowrocketNativeProxy
				}
			}
		}
	}
}

type PersonalShadowrocketDraft struct {
	SubscriptionID  int64
	ClientName      string
	RoutingPresetID int64
	Nodes           []SetPersonalSubscriptionNodeInput
	RoutingBindings RoutingBindings
	Defaults        ShadowrocketDefaults
}

// Resolve from the editor's draft using the same owner checks and source
// resolution as live generation. Only policy names and identities leave here.
func (s *Service) PreviewPersonalShadowrocketPolicies(ctx context.Context, actor PersonalSubscriptionActor,
	draft PersonalShadowrocketDraft,
) (map[string][]ShadowrocketPolicyOption, error) {
	if err := validatePersonalActor(actor); err != nil {
		return nil, err
	}
	var group PersonalSubscription
	var currentPresetID *int64
	if draft.SubscriptionID != 0 {
		var err error
		group, err = s.GetPersonalSubscription(ctx, actor, draft.SubscriptionID)
		if err != nil {
			return nil, err
		}
		currentPresetID = &group.RoutingPresetID
	}
	_, _, clientName, err := normalizePersonalSubscriptionFields("Draft", "", draft.ClientName)
	if err != nil {
		return nil, err
	}
	routingID, err := resolvePlanRoutingPresetRef(ctx, s.db, &draft.RoutingPresetID, currentPresetID)
	if err != nil {
		return nil, err
	}
	routing, err := s.GetRoutingPreset(ctx, *routingID)
	if err != nil {
		return nil, err
	}
	group.ClientName = clientName
	inputs, err := s.normalizePersonalSubscriptionNodes(ctx, actor, group, draft.Nodes, true)
	if err != nil {
		return nil, err
	}
	group.Nodes = make([]PersonalSubscriptionNode, 0, len(inputs))
	allowed := make(map[int64]struct{}, len(inputs))
	for _, input := range inputs {
		group.Nodes = append(group.Nodes, PersonalSubscriptionNode{
			ID: *input.ID, SourceType: input.SourceType, SourceID: input.SourceID,
			DisplayName: input.DisplayName, Enabled: input.Enabled, EntryHost: input.EntryHost,
			EntryPort: input.EntryPort, DialerProxy: *input.DialerProxy,
		})
		allowed[*input.ID] = struct{}{}
	}
	group.RoutingBindings, err = normalizeRoutingBindings(draft.RoutingBindings, routing.Groups, allowed)
	if err != nil {
		return nil, err
	}
	group.ShadowrocketDefaults = draft.Defaults
	data, err := s.resolvePersonalSubscriptionNodes(ctx, actor, group, routing, false)
	if err != nil {
		return nil, err
	}
	policies, err := resolveShadowrocketPolicies(data, routing, boundNodesFirst, true)
	return policies.options, err
}

func (s *Service) applyShadowrocketDefaultUpdates(ctx context.Context, actor PersonalSubscriptionActor,
	group PersonalSubscription, routing RoutingPreset, updates ShadowrocketDefaultUpdates,
) (ShadowrocketDefaults, error) {
	defaults := maps.Clone(group.ShadowrocketDefaults)
	hasSelection := false
	for key, target := range updates {
		if !validRoutingGroupKey(key) {
			return nil, ErrInvalidShadowrocketDefault
		}
		if target == nil {
			delete(defaults, key)
			continue
		}
		hasSelection = true
		defaults[key] = *target
	}
	if !hasSelection {
		return defaults, nil
	}
	group.ShadowrocketDefaults = defaults
	data, err := s.resolvePersonalSubscriptionNodes(ctx, actor, group, routing, false)
	if err != nil {
		return nil, err
	}
	policies, err := resolveShadowrocketPolicies(data, routing, boundNodesFirst, true)
	if err != nil {
		return nil, err
	}
	for key, target := range updates {
		if target != nil && !slices.ContainsFunc(policies.options[key], func(option ShadowrocketPolicyOption) bool {
			return option.Target == *target
		}) {
			return nil, fmt.Errorf("策略组 %q：%w", key, ErrInvalidShadowrocketDefault)
		}
	}
	return defaults, nil
}
