package subscription

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteLegacyIdentityUpgradePreservesSelectionAndBindings(t *testing.T) {
	db, personal := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	personal.remoteSecrets = remote.secrets
	source, err := remote.Create(t.Context(), actor, RemoteInput{Mode: "manual", Name: "Airport", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	old := `proxies:
  - {name: traffic, type: trojan, server: example.com, port: 443, password: fictional}
`
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(old)); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseRemoteSubscription([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(nodes) != 1 {
		t.Fatal("initial node missing", err)
	}
	// Simulate a cache created by the previous name-excluding algorithm.
	if _, err := db.Exec(`UPDATE remote_subscription_nodes SET identity=? WHERE id=?`, parsed.nodes[0].connectionIdentity, nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	group, err := personal.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = personal.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceRemoteNode, SourceID: nodes[0].ID, DisplayName: "Selected", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := personal.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	nodeID := group.Nodes[0].ID
	group, err = personal.SetPersonalSubscriptionRouting(t.Context(), actor, group.ID, RoutingBindings{key: {nodeID}},
		ShadowrocketDefaultUpdates{key: {Type: "node", NodeID: nodeID}})
	if err != nil {
		t.Fatal(err)
	}
	updated := `proxies:
  - {name: expire, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: 香港实验性节点, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: traffic, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: traffic, password: fictional, port: 443, server: example.com, type: trojan}
`
	for attempt := 0; attempt < 2; attempt++ {
		result, err := remote.Import(t.Context(), actor, source.ID, []byte(updated))
		if err != nil || result.NodeCount != 3 || len(result.Warnings) != 1 || result.Warnings[0] != "发现 1 个完全重复的节点，已保留第一项" {
			t.Fatalf("import count/warning: %+v %v", result, err)
		}
		current, err := remote.Nodes(t.Context(), actor, source.ID)
		if err != nil || len(current) != 3 || current[2].ID != nodes[0].ID || current[2].Name != "traffic" {
			t.Fatalf("legacy identity not preserved: %+v %v", current, err)
		}
		data, err := personal.GeneratePersonalSubscriptionData(t.Context(), group.Token)
		if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "Selected" || len(data.RoutingBindings[key]) != 1 || !strings.Contains(data.Nodes[0].RemotePayload, "fictional") {
			t.Fatalf("reference or routing binding lost: %v", err)
		}
		output, warning, err := RenderPersonalShadowrocketPreview(data)
		if err != nil || warning != "" || !strings.Contains(string(output), "policy-select-name=Selected") {
			t.Fatalf("remote default after refresh: %v %s", err, warning)
		}
	}
}

func TestRemoteIdentityReconciliationFailureKeepsCache(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	source, err := remote.Create(t.Context(), actor, RemoteInput{Mode: "manual", Name: "Airport", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte("trojan://fictional@example.com:443#old")); err != nil {
		t.Fatal(err)
	}
	before, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(before) != 1 {
		t.Fatal("initial node missing", err)
	}
	var generation int64
	if err := db.QueryRow(`SELECT cache_generation FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE remote_subscription_nodes SET payload_ciphertext='invalid' WHERE id=?`, before[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte("trojan://fictional@example.com:443#renamed")); !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("bad cache was accepted: %v", err)
	}
	var afterGeneration, count int64
	if err := db.QueryRow(`SELECT cache_generation FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&afterGeneration); err != nil || afterGeneration != generation {
		t.Fatalf("generation changed after failed reconciliation: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_nodes WHERE source_id=? AND id=?`, source.ID, before[0].ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("old node lost after failed reconciliation: %v", err)
	}
}

func TestRemoteAmbiguousRenameDoesNotReassignCachedID(t *testing.T) {
	db, personal := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	personal.remoteSecrets = remote.secrets
	source, err := remote.Create(t.Context(), actor, RemoteInput{Mode: "manual", Name: "Airport", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	before := "trojan://fictional@example.com:443#old"
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(before)); err != nil {
		t.Fatal(err)
	}
	old, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(old) != 1 {
		t.Fatal("initial node missing", err)
	}
	group, err := personal.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = personal.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceRemoteNode, SourceID: old[0].ID, DisplayName: "Selected", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	updated := "trojan://fictional@example.com:443#first\ntrojan://fictional@example.com:443#second"
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(updated)); err != nil {
		t.Fatal(err)
	}
	current, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(current) != 2 || current[0].ID == old[0].ID || current[1].ID == old[0].ID {
		t.Fatalf("ambiguous connection reused old identity: %+v %v", current, err)
	}
	group, err = personal.GetPersonalSubscription(t.Context(), actor, group.ID)
	if err != nil || len(group.Nodes) != 1 || group.Nodes[0].SourceID != old[0].ID || group.Nodes[0].Status != PersonalNodeUnavailable {
		t.Fatal("ambiguous reference was redirected or removed", err)
	}
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(updated)); err != nil {
		t.Fatal(err)
	}
	again, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(again) != 2 || again[0].ID != current[0].ID || again[1].ID != current[1].ID {
		t.Fatal("re-import accumulated or reassigned nodes", err)
	}
}

func TestRemoteLegacyURIIdentityPreservedOnImport(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	source, err := remote.Create(t.Context(), actor, RemoteInput{Mode: "manual", Name: "Airport", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	raw := "trojan://fictional@example.com:443#traffic"
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	old, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(old) != 1 {
		t.Fatal("initial node missing", err)
	}
	parsed, err := parseRemoteSubscription([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE remote_subscription_nodes SET identity=? WHERE id=?`, parsed.nodes[0].connectionIdentity, old[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte("trojan://fictional@example.com:443#expire\n"+raw)); err != nil {
		t.Fatal(err)
	}
	current, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(current) != 2 || current[1].ID != old[0].ID || current[1].Name != "traffic" {
		t.Fatalf("URI legacy identity not preserved: %+v %v", current, err)
	}
}

func TestRemoteRefreshLegacyCacheRestoresDistinctNames(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	body := "trojan://fictional@example.com:443#traffic\ntrojan://fictional@example.com:443#expire\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	remote.fetcher = remoteTestFetcher(server)
	now := time.Unix(1700000000, 0)
	remote.now = func() time.Time { return now }
	source, err := remote.Create(t.Context(), actor, RemoteInput{Name: "Airport", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	old := "trojan://fictional@example.com:443#traffic"
	if _, err := remote.Import(t.Context(), actor, source.ID, []byte(old)); err != nil {
		t.Fatal(err)
	}
	before, err := remote.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(before) != 1 {
		t.Fatal("initial cache missing", err)
	}
	parsed, err := parseRemoteSubscription([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE remote_subscription_nodes SET identity=? WHERE id=?`, parsed.nodes[0].connectionIdentity, before[0].ID); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		now = now.Add(time.Minute)
		result, err := remote.Refresh(t.Context(), actor, source.ID)
		if err != nil || result.NodeCount != 2 || len(result.Warnings) != 0 {
			t.Fatalf("refresh count: %+v %v", result, err)
		}
		after, err := remote.Nodes(t.Context(), actor, source.ID)
		if err != nil || len(after) != 2 || after[0].ID != before[0].ID || after[0].Name != "traffic" || after[1].Name != "expire" {
			t.Fatalf("refresh did not preserve old ID or restore node: %+v %v", after, err)
		}
	}
}
