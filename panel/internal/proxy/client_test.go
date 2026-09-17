package proxy

import (
	"errors"
	"testing"
)

func TestClientLifecycleKeepsUUIDAndOneClient(t *testing.T) {
	_, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Multi")
	first, err := service.GetClient(t.Context(), proxyValue.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	second, mutation, err := service.CreateClient(t.Context(), proxyValue.ID, ClientCreateInput{Name: "Laptop", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	secondUUID := second.UUID
	if second.UUID == first.UUID || mutation.Version != 3 {
		t.Fatalf("second client = %+v, mutation = %+v", second, mutation)
	}
	name := "Laptop 2"
	udp := true
	disabled := false
	second, mutation, err = service.UpdateClient(t.Context(), second.ID, ClientUpdateInput{Name: &name, ClientUDP443: &udp, Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if second.UUID != secondUUID || second.Name != name || !second.ClientUDP443 || second.Enabled || mutation.Version != 4 {
		t.Fatalf("updated client = %+v, mutation = %+v", second, mutation)
	}
	if _, err := service.DeleteClient(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteClient(t.Context(), second.ID); !errors.Is(err, ErrLastClient) {
		t.Fatalf("last client deletion error = %v", err)
	}
}
