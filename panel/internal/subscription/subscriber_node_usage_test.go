package subscription

import (
	"errors"
	"fmt"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestSubscriberNodeUsageUsesChargedBytesInPlanOrderAndResets(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	const gib int64 = 1 << 30
	multipliers := []int{100, 50, 200}
	nodes := make([]PublishedNode, 0, len(multipliers))
	for index, multiplier := range multipliers {
		serverID := int64(index + 1)
		insertSubscriptionTestServer(t, db, serverID, fmt.Sprintf("Server %d", serverID), fmt.Sprintf("203.0.113.%d", serverID))
		proxyValue := createSubscriptionTestRealityProxy(t, db, serverID, fmt.Sprintf("Proxy %d", serverID), 443)
		input := CreatePublishedNodeInput{
			Name: fmt.Sprintf("Node %d", index), Mode: NodeModeDirect, TargetProxyID: proxyValue.ID,
			TrafficMultiplierBP: multiplier, Enabled: true,
		}
		if index == 1 {
			sourceID := int64(1)
			input.Mode, input.SourceServerID = NodeModeRelay, &sourceID
		}
		node, _, err := service.CreatePublishedNode(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Usage", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	order := []int{2, 0, 1}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{nodes[2].ID, nodes[0].ID, nodes[1].ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	for serverID, clientID := range subscriberClientIDsByServer(t, db, 100) {
		for _, report := range []proxystore.ClientTrafficReport{
			{ClientID: clientID, UplinkBytes: 1000, DownlinkBytes: 2000},
			{ClientID: clientID, UplinkBytes: 1000 + 6*gib, DownlinkBytes: 2000 + 4*gib},
		} {
			if _, err := service.RecordClientTraffic(t.Context(), serverID, []proxystore.ClientTrafficReport{report}); err != nil {
				t.Fatal(err)
			}
		}
	}
	value, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || len(value.NodeUsage) != 3 || value.UsedBytes != 35*gib {
		t.Fatalf("node usage count/total = %d/%d, error = %v", len(value.NodeUsage), value.UsedBytes, err)
	}
	for position, index := range order {
		node := value.NodeUsage[position]
		multiplier := int64(multipliers[index])
		if node.PublishedNodeID != nodes[index].ID || node.Name != nodes[index].Name || node.Mode != nodes[index].Mode ||
			node.TrafficMultiplierBP != multipliers[index] || node.ChargedUplinkBytes != 6*gib*multiplier/100 ||
			node.ChargedDownlinkBytes != 4*gib*multiplier/100 || node.UsedBytes != 10*gib*multiplier/100 {
			t.Fatalf("position %d charged usage = %+v", position, node)
		}
	}
	// A new multiplier describes the node now; it must not reprice previous usage.
	updatedMultiplier := 100
	if _, _, err := service.UpdatePublishedNode(t.Context(), nodes[2].ID, UpdatePublishedNodeInput{TrafficMultiplierBP: &updatedMultiplier}); err != nil {
		t.Fatal(err)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || value.NodeUsage[0].UsedBytes != 20*gib || value.NodeUsage[0].TrafficMultiplierBP != 100 {
		t.Fatalf("usage after multiplier change = %+v, error = %v", value.NodeUsage, err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{nodes[1].ID, nodes[0].ID}); err != nil {
		t.Fatal(err)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || len(value.NodeUsage) != 2 || value.NodeUsage[0].PublishedNodeID != nodes[1].ID ||
		value.UsedBytes != 35*gib || value.UsedBytes-value.NodeUsage[0].UsedBytes-value.NodeUsage[1].UsedBytes != 20*gib {
		t.Fatalf("usage after removing node = %+v, error = %v", value, err)
	}
	value, _, err = service.ResetSubscriberTraffic(t.Context(), 100)
	if err != nil || value.UsedBytes != 0 || len(value.NodeUsage) != 2 {
		t.Fatalf("reset detail = %+v, error = %v", value, err)
	}
	for _, node := range value.NodeUsage {
		if node.UsedBytes != 0 || node.ChargedUplinkBytes != 0 || node.ChargedDownlinkBytes != 0 {
			t.Fatalf("node after reset = %+v", node)
		}
	}
}

func TestSubscriberNodeUsageEmptyAndDisabledNodes(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	value, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || value.NodeUsage == nil || len(value.NodeUsage) != 0 {
		t.Fatalf("unconfigured node usage = %+v, error = %v", value.NodeUsage, err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Empty", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	value, _, err = service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID})
	if err != nil || value.NodeUsage == nil || len(value.NodeUsage) != 0 {
		t.Fatalf("empty plan usage = %+v, error = %v", value.NodeUsage, err)
	}
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.1")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Disabled", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || len(value.NodeUsage) != 1 || value.NodeUsage[0].UsedBytes != 0 || value.ClientCount != 0 {
		t.Fatalf("node without client = %+v, error = %v", value, err)
	}
	// Corrupt legacy associations must not attribute one Client's usage twice.
	other, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Duplicate", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (?, ?, 2)`, plan.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSubscriber(t.Context(), 100); !errors.Is(err, ErrInvalidPlanNodes) {
		t.Fatalf("duplicate node usage error = %v", err)
	}
}
