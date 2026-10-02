package subscription

import (
	"encoding/json"
	"fmt"
)

func decodeRoutingBindings(raw string) (RoutingBindings, error) {
	bindings := make(RoutingBindings)
	if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
		return nil, fmt.Errorf("decode subscription routing bindings: %w", err)
	}
	if bindings == nil {
		bindings = make(RoutingBindings)
	}
	return bindings, nil
}

func encodeRoutingBindings(bindings RoutingBindings) ([]byte, error) {
	if bindings == nil {
		bindings = make(RoutingBindings)
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return nil, fmt.Errorf("encode subscription routing bindings: %w", err)
	}
	return encoded, nil
}

func normalizeRoutingBindings(bindings RoutingBindings, groups []RoutingGroup,
	allowedNodeIDs map[int64]struct{},
) (RoutingBindings, error) {
	groupNames := make(map[string]string, len(groups))
	for _, group := range groups {
		groupNames[group.Key] = group.Name
	}
	normalized := make(RoutingBindings)
	for key, nodeIDs := range bindings {
		groupName, exists := groupNames[key]
		if !exists {
			return nil, fmt.Errorf("binding 引用了当前分流方案中不存在的策略组 Key %q: %w",
				key, ErrInvalidRoutingBindings)
		}
		seen := make(map[int64]struct{}, len(nodeIDs))
		for _, nodeID := range nodeIDs {
			if nodeID <= 0 {
				return nil, fmt.Errorf("策略组 %q 的 binding 节点 ID %d 无效: %w",
					groupName, nodeID, ErrInvalidRoutingBindings)
			}
			if _, duplicate := seen[nodeID]; duplicate {
				return nil, fmt.Errorf("策略组 %q 的 binding 重复引用节点 %d: %w",
					groupName, nodeID, ErrInvalidRoutingBindings)
			}
			if _, allowed := allowedNodeIDs[nodeID]; !allowed {
				return nil, fmt.Errorf("策略组 %q 的 binding 节点 %d 不属于当前订阅: %w",
					groupName, nodeID, ErrInvalidRoutingBindings)
			}
			seen[nodeID] = struct{}{}
			normalized[key] = append(normalized[key], nodeID)
		}
		if len(normalized[key]) == 0 {
			delete(normalized, key)
		}
	}
	return normalized, nil
}

func pruneRoutingBindings(bindings RoutingBindings, allowedNodeIDs map[int64]struct{}) RoutingBindings {
	result := make(RoutingBindings)
	for key, nodeIDs := range bindings {
		for _, nodeID := range nodeIDs {
			if _, allowed := allowedNodeIDs[nodeID]; allowed {
				result[key] = append(result[key], nodeID)
			}
		}
		if len(result[key]) == 0 {
			delete(result, key)
		}
	}
	return result
}
