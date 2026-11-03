package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestCarpoolRelayUsesOwnClientAndSharedAssignments(t *testing.T) {
	f := setupUserPortalFixture(t)
	defer f.db.Close()
	server, p := createPortalServerAndProxy(t, f, proxystore.ProtocolVLESS, 8443)
	adminClient := p.Clients[0].ID
	result, err := f.db.Exec(`INSERT INTO relays(server_id,name,listen_port,entry_host_mode,entry_host,target_type,target_proxy_id,target_client_id,network,enabled,created_at,updated_at) VALUES(?, 'Relay',25001,'manual','relay.example.com','proxy',?,?,'tcp',1,1,1)`, server.Server.ID, p.ID, adminClient)
	if err != nil {
		t.Fatal(err)
	}
	relayID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/users/" + strconv.FormatInt(f.userID, 10) + "/nodes"
	for _, source := range []struct {
		kind string
		id   int64
	}{{"proxy", p.ID}, {"relay", relayID}} {
		response := performRequest(t, f.handler, http.MethodPost, path, map[string]any{"source_type": source.kind, "source_id": source.id, "name": "Alice", "user_relay_port_count": 0}, f.adminCookie)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", source.kind, response.Code, response.Body.String())
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=? AND proxy_id=?`, f.userID, p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("assigned clients=%d err=%v", count, err)
	}
	response := performRequest(t, f.handler, http.MethodGet, "/api/me/nodes", nil, f.userCookie)
	var body struct {
		Nodes []myNodeResponse `json:"nodes"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Nodes) != 2 {
		t.Fatalf("nodes: %d %s", response.Code, response.Body.String())
	}
	shares := map[string]*url.URL{}
	for _, node := range body.Nodes {
		if node.ClientID == adminClient {
			t.Fatal("admin credential reused")
		}
		share := performRequest(t, f.handler, http.MethodGet, "/api/me/nodes/"+strconv.FormatInt(node.ID, 10)+"/share", nil, f.userCookie)
		var decoded struct {
			Share struct {
				URI string `json:"uri"`
			} `json:"share"`
		}
		if share.Code != 200 || json.Unmarshal(share.Body.Bytes(), &decoded) != nil {
			t.Fatalf("share request or decoding failed: %d", share.Code)
		}
		parsed, err := url.Parse(decoded.Share.URI)
		if err != nil {
			t.Fatal(err)
		}
		shares[node.SourceType] = parsed
		denied := performRequest(t, f.handler, http.MethodGet, "/api/me/nodes/"+strconv.FormatInt(node.ID, 10)+"/share", nil, f.otherCookie)
		if denied.Code != 404 {
			t.Fatalf("other user share=%d", denied.Code)
		}
		denied = performRequest(t, f.handler, http.MethodPatch, "/api/me/nodes/"+strconv.FormatInt(node.ID, 10), map[string]any{"name": "intruder"}, f.otherCookie)
		if denied.Code != http.StatusNotFound {
			t.Fatalf("other user update=%d", denied.Code)
		}
	}
	if shares["proxy"].User.String() != shares["relay"].User.String() || shares["relay"].Host != "relay.example.com:25001" || shares["proxy"].Host == shares["relay"].Host {
		t.Fatalf("unexpected endpoints or credentials")
	}
	adminShare, err := proxystore.NewService(f.db).GetClientShare(t.Context(), adminClient)
	if err != nil {
		t.Fatal(err)
	}
	adminURI, err := url.Parse(adminShare.URI)
	if err != nil {
		t.Fatal(err)
	}
	if shares["relay"].User.String() == adminURI.User.String() {
		t.Fatal("relay leaked target client credential")
	}
	relays := relaystore.NewService(f.db)
	manual := relaystore.TargetManual
	host, port := "example.com", 443
	if _, _, err := relays.Update(t.Context(), relayID, relaystore.UpdateInput{TargetType: &manual, TargetHost: &host, TargetPort: &port}); !errors.Is(err, relaystore.ErrCarpoolReferenced) {
		t.Fatalf("target change: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE relays SET enabled=0 WHERE id=?`, relayID); err != nil {
		t.Fatal(err)
	}
	disabled := performRequest(t, f.handler, http.MethodGet, "/api/me/nodes", nil, f.userCookie)
	var disabledBody struct {
		Nodes []myNodeResponse `json:"nodes"`
	}
	if json.Unmarshal(disabled.Body.Bytes(), &disabledBody) != nil {
		t.Fatal("decode nodes")
	}
	for _, node := range disabledBody.Nodes {
		if node.SourceType == "relay" {
			if node.Status != "relay_disabled" || node.EffectiveEnabled {
				t.Fatalf("disabled status: %+v", node)
			}
			share := performRequest(t, f.handler, http.MethodGet, "/api/me/nodes/"+strconv.FormatInt(node.ID, 10)+"/share", nil, f.userCookie)
			if share.Code != http.StatusConflict {
				t.Fatalf("disabled relay share: %d", share.Code)
			}
		}
	}
	detail := performRequest(t, f.handler, http.MethodGet, "/api/admin/users/"+strconv.FormatInt(f.otherID, 10), nil, f.adminCookie)
	var detailBody struct {
		Available []carpoolSource `json:"available_sources"`
	}
	if json.Unmarshal(detail.Body.Bytes(), &detailBody) != nil {
		t.Fatal("decode sources")
	}
	for _, source := range detailBody.Available {
		if source.SourceType == "relay" && source.SourceID == relayID {
			t.Fatal("disabled relay offered")
		}
	}
	if _, err := f.db.Exec(`UPDATE relays SET enabled=1 WHERE id=?`, relayID); err != nil {
		t.Fatal(err)
	}
	deletion := performRequest(t, f.handler, http.MethodDelete, path+"/"+strconv.FormatInt(body.Nodes[0].ID, 10), nil, f.adminCookie)
	if deletion.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deletion.Code, deletion.Body.String())
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=?`, f.userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared client lost: %d %v", count, err)
	}
	if _, err := relays.Delete(t.Context(), relayID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM carpool_node_assignments WHERE source_type='relay' AND source_id=?`, relayID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("relay assignments remain=%d err=%v", count, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=?`, f.userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("historical-safe client retention=%d err=%v", count, err)
	}
}

func TestCarpoolRelayReusesEarliestHistoricalClient(t *testing.T) {
	f := setupUserPortalFixture(t)
	defer f.db.Close()
	server, p := createPortalServerAndProxy(t, f, proxystore.ProtocolVLESS, 8443)
	first := p.Clients[0].ID
	resp := performRequest(t, f.handler, http.MethodPost, "/api/proxies/"+strconv.FormatInt(p.ID, 10)+"/clients", map[string]any{"name": "second", "enabled": true}, f.adminCookie)
	var added struct {
		Client clientResponse `json:"client"`
	}
	if resp.Code != http.StatusCreated || json.Unmarshal(resp.Body.Bytes(), &added) != nil {
		t.Fatalf("client: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := f.db.Exec(`UPDATE clients SET assigned_user_id=? WHERE id IN (?,?)`, f.userID, first, added.Client.ID); err != nil {
		t.Fatal(err)
	}
	result, err := f.db.Exec(`INSERT INTO relays(server_id,name,listen_port,entry_host_mode,entry_host,target_type,target_proxy_id,target_client_id,network,enabled,created_at,updated_at) VALUES(?, 'Relay',25001,'manual','relay.example.com','proxy',?,?,'tcp',1,1,1)`, server.Server.ID, p.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	relayID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	resp = performRequest(t, f.handler, http.MethodPost, "/api/admin/users/"+strconv.FormatInt(f.userID, 10)+"/nodes", map[string]any{"source_type": "relay", "source_id": relayID, "name": "ignored", "user_relay_port_count": 0}, f.adminCookie)
	if resp.Code != http.StatusCreated {
		t.Fatalf("assignment: %d %s", resp.Code, resp.Body.String())
	}
	var chosen, count int64
	if err := f.db.QueryRow(`SELECT client_id FROM carpool_node_assignments WHERE user_id=? AND source_type='relay' AND source_id=?`, f.userID, relayID).Scan(&chosen); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=?`, f.userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if chosen != first || count != 2 {
		t.Fatalf("chosen=%d want=%d count=%d", chosen, first, count)
	}
}

func TestCarpoolRelayEligibility(t *testing.T) {
	proxyID := int64(7)
	valid := relaystore.Relay{TargetType: relaystore.TargetProxy, TargetProxyID: &proxyID, EntryAddress: "relay.example.com", ListenPort: 25001, TargetAddressReady: true, Enabled: true, Network: relaystore.NetworkTCP}
	owner := int64(3)
	cases := []struct {
		name   string
		change func(*relaystore.Relay)
	}{
		{"disabled", func(r *relaystore.Relay) { r.Enabled = false }},
		{"owned", func(r *relaystore.Relay) { r.OwnerUserID = &owner }},
		{"subscription", func(r *relaystore.Relay) { r.SubscriptionPublished = true }},
		{"manual", func(r *relaystore.Relay) { r.TargetType = relaystore.TargetManual }},
		{"landing", func(r *relaystore.Relay) { r.TargetType = relaystore.TargetLanding }},
		{"missing proxy", func(r *relaystore.Relay) { r.TargetProxyID = nil }},
		{"missing entry", func(r *relaystore.Relay) { r.EntryAddress = "" }},
		{"target unavailable", func(r *relaystore.Relay) { r.TargetAddressReady = false }},
		{"network incompatible", func(r *relaystore.Relay) { r.Network = relaystore.NetworkUDP }},
	}
	if !relaystore.CarpoolAvailable(valid, proxyID, proxystore.ProtocolVLESS) {
		t.Fatal("valid relay rejected")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.change(&r)
			if relaystore.CarpoolAvailable(r, proxyID, proxystore.ProtocolVLESS) {
				t.Fatal("ineligible relay accepted")
			}
		})
	}
}

func TestCarpoolConcurrentRelayAssignmentsShareClient(t *testing.T) {
	f := setupUserPortalFixture(t)
	defer f.db.Close()
	server, p := createPortalServerAndProxy(t, f, proxystore.ProtocolVLESS, 8443)
	ids := make([]int64, 2)
	for i := range ids {
		result, err := f.db.Exec(`INSERT INTO relays(server_id,name,listen_port,entry_host_mode,entry_host,target_type,target_proxy_id,target_client_id,network,enabled,created_at,updated_at) VALUES(?, ?,?,'manual','relay.example.com','proxy',?,?,'tcp',1,1,1)`, server.Server.ID, "relay"+strconv.Itoa(i), 25001+i, p.ID, p.Clients[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		ids[i], err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/admin/users/" + strconv.FormatInt(f.userID, 10) + "/nodes"
	start := make(chan struct{})
	results := make(chan int, 3)
	for _, id := range []int64{ids[0], ids[0], ids[1]} {
		go func(id int64) {
			<-start
			response := performRequest(t, f.handler, http.MethodPost, path, map[string]any{"source_type": "relay", "source_id": id, "name": "Alice", "user_relay_port_count": 0}, f.adminCookie)
			results <- response.Code
		}(id)
	}
	close(start)
	created, conflict := 0, 0
	for range 3 {
		code := <-results
		if code == http.StatusCreated {
			created++
		} else if code == http.StatusConflict {
			conflict++
		} else {
			t.Fatalf("unexpected response: %d", code)
		}
	}
	if created != 2 || conflict != 1 {
		t.Fatalf("created=%d conflict=%d", created, conflict)
	}
	var clients, assignments int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=? AND proxy_id=?`, f.userID, p.ID).Scan(&clients); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM carpool_node_assignments WHERE user_id=?`, f.userID).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if clients != 1 || assignments != 2 {
		t.Fatalf("clients=%d assignments=%d", clients, assignments)
	}
}

func TestCarpoolAssignmentFailurePreservesUserRelay(t *testing.T) {
	f := setupUserPortalFixture(t)
	defer f.db.Close()
	server, p := createPortalServerAndProxy(t, f, proxystore.ProtocolVLESS, 8443)
	c := createManagedPortalClient(t, f, p.ID, "Alice", nil)
	result, err := f.db.Exec(`INSERT INTO relays(server_id,owner_user_id,source_client_id,name,listen_port,entry_host_mode,entry_host,target_type,target_host,target_port,network,enabled,created_at,updated_at) VALUES(?,?,?,'owned',25001,'manual','owned.example.com','manual','1.1.1.1',443,'tcp',1,1,1)`, server.Server.ID, f.userID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	relayID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/users/" + strconv.FormatInt(f.userID, 10) + "/nodes"
	for _, payload := range []map[string]any{{"source_type": "proxy", "source_id": p.ID, "name": "duplicate"}, {"source_type": "relay", "source_id": relayID, "name": "owned relay"}} {
		response := performRequest(t, f.handler, http.MethodPost, path, payload, f.adminCookie)
		if response.Code < 400 {
			t.Fatalf("invalid assignment accepted: %d", response.Code)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM relays WHERE id=? AND owner_user_id=? AND source_client_id=?`, relayID, f.userID, c.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("owned relay lost: count=%d err=%v", count, err)
	}
}
