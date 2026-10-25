package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestSourceDeletePrunesAllPersonalReferencesAtomically(t *testing.T) {
	for _, sourceType := range []string{"proxy", "relay", "landing"} {
		for _, failure := range []string{"", "source", "bindings"} {
			t.Run(sourceType+"/"+failure, func(t *testing.T) {
				f := newProxyDeleteFixture(t)
				serverID := f.createServer(t, "source")
				if _, err := f.db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
					VALUES (2, 'vip', 'hash', 'vip', 1, 1)`); err != nil {
					t.Fatal(err)
				}
				landings := landingstore.NewService(f.db)
				createLanding := func(name string) landingstore.Landing {
					t.Helper()
					value, err := landings.Create(t.Context(), 1, landingstore.CreateInput{
						Name: name, Visibility: landingstore.VisibilityPublic, URI: "vless://uuid@external.example.com:443",
					})
					if err != nil {
						t.Fatal(err)
					}
					return value
				}
				keep := createLanding("keep")
				var sourceID int64
				table, endpoint := "proxies", "proxies"
				switch sourceType {
				case "proxy":
					sourceID = f.createProxy(t, serverID, 443, "delete").ID
				case "relay":
					value, _, err := relaystore.NewService(f.db).Create(t.Context(), relaystore.CreateInput{
						ServerID: serverID, Name: "delete", ListenPort: 9000, EntryHostMode: relaystore.EntryHostManual, EntryHost: "entry.example.com",
						TargetType: relaystore.TargetLanding, TargetLandingID: &keep.ID, Network: relaystore.NetworkTCP, Enabled: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					sourceID, table, endpoint = value.ID, "relays", "relays"
				case "landing":
					sourceID, table, endpoint = createLanding("delete").ID, "landing_nodes", "landings"
				}
				preset, err := f.subscriptions.CreateRoutingPreset(t.Context(), subscriptionstore.CreateRoutingPresetInput{
					Name: "bindings", Enabled: true,
					Groups: []subscriptionstore.RoutingGroup{{Name: "Mixed", Type: "select"}, {Name: "Keep", Type: "select"}, {Name: "Removed", Type: "select"}},
					Rules:  []string{"MATCH,Mixed"},
				})
				if err != nil {
					t.Fatal(err)
				}
				groups := make([]subscriptionstore.PersonalSubscription, 0, 3)
				actors := []subscriptionstore.PersonalSubscriptionActor{{UserID: 1, Role: "admin"}, {UserID: 2, Role: "vip"}, {UserID: 1, Role: "admin"}}
				for i, actor := range actors {
					group, err := f.subscriptions.CreatePersonalSubscription(t.Context(), actor, subscriptionstore.CreatePersonalSubscriptionInput{
						Name: fmt.Sprintf("group-%d", i), ClientName: "default", RoutingPresetID: &preset.ID, Enabled: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					inputs := []subscriptionstore.SetPersonalSubscriptionNodeInput{
						{SourceType: "landing", SourceID: keep.ID, DisplayName: "keep", Enabled: true},
					}
					if i < 2 {
						hostA, hostB, portA, portB := "first.example.com", "second.example.com", 8443, 9443
						inputs = append([]subscriptionstore.SetPersonalSubscriptionNodeInput{
							{SourceType: sourceType, SourceID: sourceID, DisplayName: "first", EntryHost: &hostA, EntryPort: &portA, Enabled: true},
							{SourceType: sourceType, SourceID: sourceID, DisplayName: "second", EntryHost: &hostB, EntryPort: &portB, Enabled: false},
						}, inputs...)
					}
					group, err = f.subscriptions.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, inputs)
					if err != nil {
						t.Fatal(err)
					}
					keepID := group.Nodes[len(group.Nodes)-1].ID
					bindings := subscriptionstore.RoutingBindings{preset.Groups[0].Key: {keepID}, preset.Groups[1].Key: {keepID}}
					if i < 2 {
						bindings[preset.Groups[0].Key] = []int64{group.Nodes[0].ID, keepID, group.Nodes[1].ID}
						bindings[preset.Groups[2].Key] = []int64{group.Nodes[1].ID, group.Nodes[0].ID}
					}
					group, err = f.subscriptions.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, group.ID, bindings)
					if err != nil {
						t.Fatal(err)
					}
					groups = append(groups, group)
				}
				var versionBefore int64
				if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&versionBefore); err != nil {
					t.Fatal(err)
				}
				if failure != "" {
					trigger := fmt.Sprintf(`CREATE TRIGGER reject_source_delete BEFORE DELETE ON %s
						BEGIN SELECT RAISE(ABORT, 'source deletion rejected'); END`, table)
					if failure == "bindings" {
						trigger = `CREATE TRIGGER reject_binding_cleanup BEFORE UPDATE OF routing_bindings_json ON personal_subscription_groups
							BEGIN SELECT RAISE(ABORT, 'binding cleanup rejected'); END`
					}
					if _, err := f.db.Exec(trigger); err != nil {
						t.Fatal(err)
					}
				}
				response := performRequest(t, f.handler, http.MethodDelete, fmt.Sprintf("/api/%s/%d", endpoint, sourceID), nil, f.cookie)
				wantStatus, wantSourceCount, wantReferences := http.StatusNoContent, 0, 0
				if failure != "" {
					wantStatus, wantSourceCount, wantReferences = http.StatusInternalServerError, 1, 4
				}
				if response.Code != wantStatus {
					t.Fatalf("delete status=%d, want %d: %s", response.Code, wantStatus, response.Body.String())
				}
				assertDatabaseCount(t, f.db, "SELECT COUNT(*) FROM "+table+" WHERE id = ?", wantSourceCount, sourceID)
				assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM personal_subscription_nodes WHERE source_type = ? AND source_id = ?`, wantReferences, sourceType, sourceID)
				for i, before := range groups {
					after, err := f.subscriptions.GetPersonalSubscription(t.Context(), actors[i], before.ID)
					if err != nil {
						t.Fatal(err)
					}
					wantNodes, wantBindings := before.Nodes, before.RoutingBindings
					if failure == "" && i < 2 {
						wantNodes = before.Nodes[2:]
						wantBindings = subscriptionstore.RoutingBindings{
							preset.Groups[0].Key: {wantNodes[0].ID}, preset.Groups[1].Key: {wantNodes[0].ID},
						}
					}
					if !reflect.DeepEqual(after.Nodes, wantNodes) || !reflect.DeepEqual(after.RoutingBindings, wantBindings) {
						t.Fatalf("group %d nodes or bindings changed incorrectly", before.ID)
					}
					if (failure != "" || i == 2) && after.UpdatedAt != before.UpdatedAt {
						t.Fatalf("unaffected or rolled-back group %d timestamp changed", before.ID)
					}
				}
				if failure != "" {
					var versionAfter int64
					if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&versionAfter); err != nil || versionAfter != versionBefore {
						t.Fatalf("failed deletion changed desired state version: %v", err)
					}
				}
			})
		}
	}
}

func TestPersonalSourcesAPIRejectsDisabledSourceAndRefreshesAfterDelete(t *testing.T) {
	for _, sourceType := range []string{"proxy", "relay"} {
		t.Run(sourceType, func(t *testing.T) {
			f := newProxyDeleteFixture(t)
			serverID := f.createServer(t, "source")
			proxy := f.createProxy(t, serverID, 443, "proxy")
			sourceID, table, endpoint := proxy.ID, "proxies", "proxies"
			if sourceType == "relay" {
				value, _, err := relaystore.NewService(f.db).Create(t.Context(), relaystore.CreateInput{
					ServerID: serverID, Name: "relay", ListenPort: 9000, EntryHostMode: relaystore.EntryHostManual, EntryHost: "entry.example.com",
					TargetType: relaystore.TargetProxy, TargetProxyID: &proxy.ID, TargetClientID: &proxy.Clients[0].ID, Network: relaystore.NetworkTCP, Enabled: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				sourceID, table, endpoint = value.ID, "relays", "relays"
			}
			assertCandidate := func(want bool) {
				t.Helper()
				response := performRequest(t, f.handler, http.MethodGet, "/api/personal-subscriptions/sources?client_name=default", nil, f.cookie)
				var body struct {
					Sources []struct {
						SourceType string `json:"source_type"`
						SourceID   int64  `json:"source_id"`
					} `json:"sources"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
					t.Fatalf("sources status=%d, decode error=%v", response.Code, err)
				}
				found := false
				for _, value := range body.Sources {
					found = found || value.SourceType == sourceType && value.SourceID == sourceID
				}
				if found != want {
					t.Fatalf("candidate present=%v, want %v", found, want)
				}
			}
			assertCandidate(true)
			if _, err := f.db.Exec("UPDATE "+table+" SET enabled = 0 WHERE id = ?", sourceID); err != nil {
				t.Fatal(err)
			}
			assertCandidate(false)
			group, err := f.subscriptions.CreatePersonalSubscription(t.Context(), subscriptionstore.PersonalSubscriptionActor{UserID: 1, Role: "admin"},
				subscriptionstore.CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			response := performRequest(t, f.handler, http.MethodPut, fmt.Sprintf("/api/personal-subscriptions/%d/nodes", group.ID), map[string]any{
				"nodes": []map[string]any{{"source_type": sourceType, "source_id": sourceID, "display_name": "bypass", "enabled": true}},
			}, f.cookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("disabled source accepted by API: status=%d", response.Code)
			}
			assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM personal_subscription_nodes WHERE group_id = ?`, 0, group.ID)
			response = performRequest(t, f.handler, http.MethodDelete, fmt.Sprintf("/api/%s/%d", endpoint, sourceID), nil, f.cookie)
			if response.Code != http.StatusNoContent {
				t.Fatalf("delete status=%d: %s", response.Code, response.Body.String())
			}
			assertCandidate(false)
		})
	}
}

func TestUserDeletePrunesOtherOwnersPersonalSourceReferences(t *testing.T) {
	f := newProxyDeleteFixture(t)
	_, ownerID := registerAccount(t, f.db, f.handler, f.cookie, "vip", "landing-owner")
	serverID := f.createServer(t, "source")
	landings := landingstore.NewService(f.db)
	external, err := landings.Create(t.Context(), ownerID, landingstore.CreateInput{
		Name: "owned", Visibility: landingstore.VisibilityPublic, URI: "vless://uuid@external.example.com:443",
	})
	if err != nil {
		t.Fatal(err)
	}
	local, _, err := relaystore.NewService(f.db).Create(t.Context(), relaystore.CreateInput{
		ServerID: serverID, Name: "local", ListenPort: 9000, EntryHostMode: relaystore.EntryHostManual, EntryHost: "entry.example.com",
		TargetType: relaystore.TargetLanding, TargetLandingID: &external.ID, Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := landings.Create(t.Context(), 1, landingstore.CreateInput{Name: "keep", URI: "vless://uuid@keep.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	actor := subscriptionstore.PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	group, err := f.subscriptions.CreatePersonalSubscription(t.Context(), actor, subscriptionstore.CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = f.subscriptions.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []subscriptionstore.SetPersonalSubscriptionNodeInput{
		{SourceType: "landing", SourceID: external.ID, DisplayName: "owned", Enabled: true},
		{SourceType: "relay", SourceID: local.ID, DisplayName: "relay", Enabled: true},
		{SourceType: "landing", SourceID: keep.ID, DisplayName: "keep", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := f.subscriptions.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key, keepID := preset.Groups[0].Key, group.Nodes[2].ID
	if _, err := f.subscriptions.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, group.ID,
		subscriptionstore.RoutingBindings{key: {group.Nodes[0].ID, keepID, group.Nodes[1].ID}}); err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, f.handler, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", ownerID), nil, f.cookie)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete user status=%d: %s", response.Code, response.Body.String())
	}
	stored, err := f.subscriptions.GetPersonalSubscription(t.Context(), actor, group.ID)
	if err != nil || len(stored.Nodes) != 1 || stored.Nodes[0].ID != keepID ||
		!reflect.DeepEqual(stored.RoutingBindings, subscriptionstore.RoutingBindings{key: {keepID}}) {
		t.Fatalf("user deletion left personal references or bindings: %v", err)
	}
	assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM landing_nodes WHERE id = ?`, 0, external.ID)
	assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM relays WHERE id = ?`, 0, local.ID)
}
