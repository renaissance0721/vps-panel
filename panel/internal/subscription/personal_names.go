package subscription

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func personalReservedNodeNames(groups []RoutingGroup) map[string]bool {
	names := map[string]bool{
		"DIRECT": true, "REJECT": true, shadowrocketNativeProxy: true,
		"REJECT-DROP": true, "PASS": true, "PASS-RULE": true, "COMPATIBLE": true, "GLOBAL": true,
	}
	for _, group := range groups {
		names[group.Name] = true
	}
	return names
}

func personalSourceNodeName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, name))
	if name == "" {
		return "节点"
	}
	return name
}

// Assign names by stable instance ID while retaining the configured output order.
// New draft instances follow their submitted positions, like the IDs assigned on save.
func resolvePersonalDisplayNames(nodes []PersonalSubscriptionNode, groups []RoutingGroup) {
	used := personalReservedNodeNames(groups)
	var automatic []int
	for i := range nodes {
		node := &nodes[i]
		node.DisplayName = node.CustomName
		if node.SourceType == PersonalSourceRemoteSource {
			if node.DisplayName == "" {
				node.DisplayName = personalSourceNodeName(node.SourceName)
			}
			continue
		}
		if node.DisplayName == "" || used[node.DisplayName] || !safeShadowrocketValue(node.DisplayName) {
			automatic = append(automatic, i)
			continue
		}
		used[node.DisplayName] = true
	}
	rank := func(id int64) int64 {
		if id <= 0 {
			return math.MaxInt64
		}
		return id
	}
	slices.SortStableFunc(automatic, func(a, b int) int { return cmp.Compare(rank(nodes[a].ID), rank(nodes[b].ID)) })
	var collisions []int
	for _, i := range automatic {
		node := &nodes[i]
		base := node.CustomName
		if base == "" {
			base = node.SourceName
		}
		node.DisplayName = personalSourceNodeName(base)
		if used[node.DisplayName] {
			collisions = append(collisions, i)
			continue
		}
		used[node.DisplayName] = true
	}
	// Reserve all unsuffixed names before numbering collisions so a suffix
	// never takes a name such as "Japan [2]" from its original source.
	lastSuffix := make(map[string]int)
	for _, i := range collisions {
		base := nodes[i].DisplayName
		for suffix := max(2, lastSuffix[base]+1); ; suffix++ {
			name := fmt.Sprintf("%s [%d]", base, suffix)
			if !used[name] {
				nodes[i].DisplayName = name
				used[name] = true
				lastSuffix[base] = suffix
				break
			}
		}
	}
}

func validPersonalCustomName(name string) bool {
	return safeShadowrocketValue(name) && utf8.RuneCountInString(name) <= maxNameRunes
}
