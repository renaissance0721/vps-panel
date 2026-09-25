package proxy

import (
	"testing"
)

func TestClientLifecycleKeepsUUIDAndAllowsDeletingLastClient(t *testing.T) {
	db, service, serverID := newTestService(t)
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
	if _, err := service.DeleteClient(t.Context(), second.ID); err != nil {
		t.Fatalf("delete last client: %v", err)
	}
	var proxyCount, clientCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id = ?`, proxyValue.ID).Scan(&proxyCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients WHERE proxy_id = ?`, proxyValue.ID).Scan(&clientCount); err != nil {
		t.Fatal(err)
	}
	if proxyCount != 1 || clientCount != 0 {
		t.Fatalf("last Client delete counts = proxy %d, clients %d", proxyCount, clientCount)
	}
}
