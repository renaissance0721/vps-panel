package proxy

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestClientTrafficBaselineDeltaResetAndActivity(t *testing.T) {
	_, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Traffic")
	clientID := proxyValue.Clients[0].ID
	now := time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return now }

	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 100, DownlinkBytes: 200,
	}}); err != nil {
		t.Fatal(err)
	}
	metrics := readClientMetrics(t, service, clientID)
	if metrics.XrayUplinkBytes != 100 || metrics.XrayDownlinkBytes != 200 ||
		metrics.CycleUplinkBytes != 0 || metrics.CycleDownlinkBytes != 0 || metrics.LastActivityAt != nil ||
		!metrics.CycleStartedAt.Equal(now) {
		t.Fatalf("initial metrics = %+v", metrics)
	}

	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 150, DownlinkBytes: 260,
	}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.CycleUplinkBytes != 50 || metrics.CycleDownlinkBytes != 60 ||
		metrics.LastActivityAt == nil || !metrics.LastActivityAt.Equal(now) {
		t.Fatalf("normal delta metrics = %+v", metrics)
	}

	lastActivity := *metrics.LastActivityAt
	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 150, DownlinkBytes: 260,
	}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.LastActivityAt == nil || !metrics.LastActivityAt.Equal(lastActivity) || !metrics.UpdatedAt.Equal(now) {
		t.Fatalf("zero delta activity = %+v", metrics)
	}

	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 10, DownlinkBytes: 300,
	}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.XrayUplinkBytes != 10 || metrics.XrayDownlinkBytes != 300 ||
		metrics.CycleUplinkBytes != 50 || metrics.CycleDownlinkBytes != 100 ||
		metrics.LastActivityAt == nil || !metrics.LastActivityAt.Equal(now) {
		t.Fatalf("independent reset metrics = %+v", metrics)
	}

	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 15, DownlinkBytes: 5,
	}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.CycleUplinkBytes != 55 || metrics.CycleDownlinkBytes != 100 {
		t.Fatalf("opposite direction reset metrics = %+v", metrics)
	}
}

func TestClientTrafficRejectsInvalidAndCrossServerReportsAtomically(t *testing.T) {
	_, service, serverID := newTestService(t)
	first := createRealityProxy(t, service, serverID, 443, "First")
	result, err := service.db.Exec(`INSERT INTO servers (name, status, created_at, updated_at) VALUES ('other', 'offline', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	otherServerID, _ := result.LastInsertId()
	second := createRealityProxy(t, service, otherServerID, 443, "Second")

	for name, reports := range map[string][]ClientTrafficReport{
		"negative":  {{ClientID: first.Clients[0].ID, UplinkBytes: -1}},
		"duplicate": {{ClientID: first.Clients[0].ID}, {ClientID: first.Clients[0].ID}},
		"unknown":   {{ClientID: 999999, UplinkBytes: 1, DownlinkBytes: 1}},
		"other server": {
			{ClientID: first.Clients[0].ID, UplinkBytes: 10, DownlinkBytes: 10},
			{ClientID: second.Clients[0].ID, UplinkBytes: 10, DownlinkBytes: 10},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := service.RecordClientTraffic(t.Context(), serverID, reports); !errors.Is(err, ErrInvalidClientTraffic) {
				t.Fatalf("invalid report error = %v", err)
			}
			var count int
			if err := service.db.QueryRow(`SELECT COUNT(*) FROM client_metrics`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid report left %d metrics rows", count)
			}
		})
	}
}

func TestClientTrafficCascadesWhenClientIsDeleted(t *testing.T) {
	db, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Client deletion")
	second, _, err := service.CreateClient(t.Context(), proxyValue.ID, ClientCreateInput{Name: "Keep", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	firstID := proxyValue.Clients[0].ID
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{
		{ClientID: firstID, UplinkBytes: 1, DownlinkBytes: 2},
		{ClientID: second.ID, UplinkBytes: 3, DownlinkBytes: 4},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteClient(t.Context(), firstID); err != nil {
		t.Fatal(err)
	}
	var deletedCount, remainingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM client_metrics WHERE client_id = ?`, firstID).Scan(&deletedCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM client_metrics WHERE client_id = ?`, second.ID).Scan(&remainingCount); err != nil {
		t.Fatal(err)
	}
	if deletedCount != 0 || remainingCount != 1 {
		t.Fatalf("metrics after client deletion = deleted %d, remaining %d", deletedCount, remainingCount)
	}
}

func TestClientTrafficPersistsAcrossPanelRestartAndCascades(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO servers (name, status, created_at, updated_at) VALUES ('restart', 'offline', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	serverID, _ := result.LastInsertId()
	service := NewService(db)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Restart")
	clientID := proxyValue.Clients[0].ID
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100, DownlinkBytes: 200}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service = NewService(db)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 130, DownlinkBytes: 250}}); err != nil {
		t.Fatal(err)
	}
	metrics := readClientMetrics(t, service, clientID)
	if metrics.CycleUplinkBytes != 30 || metrics.CycleDownlinkBytes != 50 {
		t.Fatalf("metrics after restart = %+v", metrics)
	}
	if _, err := service.Delete(t.Context(), proxyValue.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM client_metrics WHERE client_id = ?`, clientID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("proxy deletion left %d client metric rows", count)
	}
}

func TestVLESSAndShadowsocksClientsAccumulateIndependently(t *testing.T) {
	_, service, serverID := newTestService(t)
	vless := createRealityProxy(t, service, serverID, 443, "VLESS")
	vlessSecond, _, err := service.CreateClient(t.Context(), vless.ID, ClientCreateInput{Name: "VLESS B", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	shadowsocks, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "Shadowsocks", Protocol: ProtocolShadowsocks,
		Method: ShadowsocksMethodAES128GCM, ListenPort: 8388, EntryHostMode: EntryHostAuto,
		Enabled: true, FirstClientName: "SS Client",
	})
	if err != nil {
		t.Fatal(err)
	}
	shadowsocksSecond, _, err := service.CreateClient(t.Context(), shadowsocks.ID, ClientCreateInput{Name: "SS B", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	reports := []ClientTrafficReport{
		{ClientID: vless.Clients[0].ID, UplinkBytes: 10, DownlinkBytes: 20},
		{ClientID: vlessSecond.ID, UplinkBytes: 30, DownlinkBytes: 40},
		{ClientID: shadowsocks.Clients[0].ID, UplinkBytes: 100, DownlinkBytes: 200},
		{ClientID: shadowsocksSecond.ID, UplinkBytes: 300, DownlinkBytes: 400},
	}
	if err := service.RecordClientTraffic(t.Context(), serverID, reports); err != nil {
		t.Fatal(err)
	}
	reports[0].UplinkBytes, reports[0].DownlinkBytes = 15, 27
	reports[1].UplinkBytes, reports[1].DownlinkBytes = 41, 53
	reports[2].UplinkBytes, reports[2].DownlinkBytes = 130, 240
	reports[3].UplinkBytes, reports[3].DownlinkBytes = 350, 460
	if err := service.RecordClientTraffic(t.Context(), serverID, reports); err != nil {
		t.Fatal(err)
	}
	vlessMetrics := readClientMetrics(t, service, vless.Clients[0].ID)
	vlessSecondMetrics := readClientMetrics(t, service, vlessSecond.ID)
	shadowsocksMetrics := readClientMetrics(t, service, shadowsocks.Clients[0].ID)
	shadowsocksSecondMetrics := readClientMetrics(t, service, shadowsocksSecond.ID)
	if vlessMetrics.CycleUplinkBytes != 5 || vlessMetrics.CycleDownlinkBytes != 7 ||
		vlessSecondMetrics.CycleUplinkBytes != 11 || vlessSecondMetrics.CycleDownlinkBytes != 13 ||
		shadowsocksMetrics.CycleUplinkBytes != 30 || shadowsocksMetrics.CycleDownlinkBytes != 40 ||
		shadowsocksSecondMetrics.CycleUplinkBytes != 50 || shadowsocksSecondMetrics.CycleDownlinkBytes != 60 {
		t.Fatalf("independent metrics = VLESS A %+v, VLESS B %+v, Shadowsocks A %+v, Shadowsocks B %+v",
			vlessMetrics, vlessSecondMetrics, shadowsocksMetrics, shadowsocksSecondMetrics)
	}
}

func TestClientRenameDoesNotChangeStatsIdentifier(t *testing.T) {
	db, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Rename")
	clientID := proxyValue.Clients[0].ID
	before := desiredClientStatsID(t, db, serverID, clientID)
	name := "Renamed Client"
	if _, _, err := service.UpdateClient(t.Context(), clientID, ClientUpdateInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	after := desiredClientStatsID(t, db, serverID, clientID)
	if before != "vp-client-"+strconv.FormatInt(clientID, 10) || after != before {
		t.Fatalf("stats identifier changed from %q to %q", before, after)
	}
}

func TestClientTrafficCycleBoundariesUseShanghaiTime(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	tests := []struct {
		name      string
		now       time.Time
		config    ClientTrafficConfig
		wantStart time.Time
		wantNext  time.Time
	}{
		{
			name:      "daily",
			now:       time.Date(2026, 9, 13, 0, 30, 0, 0, shanghai),
			config:    ClientTrafficConfig{ResetMode: TrafficResetDaily, Weekday: 1, Day: 1, ResetTime: "00:00"},
			wantStart: time.Date(2026, 9, 13, 0, 0, 0, 0, shanghai),
			wantNext:  time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai),
		},
		{
			name:      "weekly Monday",
			now:       time.Date(2026, 9, 16, 12, 0, 0, 0, shanghai),
			config:    ClientTrafficConfig{ResetMode: TrafficResetWeekly, Weekday: 1, Day: 1, ResetTime: "06:30"},
			wantStart: time.Date(2026, 9, 14, 6, 30, 0, 0, shanghai),
			wantNext:  time.Date(2026, 9, 21, 6, 30, 0, 0, shanghai),
		},
		{
			name:      "monthly day 29 clamps in non-leap February",
			now:       time.Date(2027, 3, 1, 12, 0, 0, 0, shanghai),
			config:    ClientTrafficConfig{ResetMode: TrafficResetMonthly, Weekday: 1, Day: 29, ResetTime: "08:00"},
			wantStart: time.Date(2027, 2, 28, 8, 0, 0, 0, shanghai),
			wantNext:  time.Date(2027, 3, 29, 8, 0, 0, 0, shanghai),
		},
		{
			name:      "monthly day 31 clamps to February",
			now:       time.Date(2027, 3, 1, 12, 0, 0, 0, shanghai),
			config:    ClientTrafficConfig{ResetMode: TrafficResetMonthly, Weekday: 1, Day: 31, ResetTime: "00:00"},
			wantStart: time.Date(2027, 2, 28, 0, 0, 0, 0, shanghai),
			wantNext:  time.Date(2027, 3, 31, 0, 0, 0, 0, shanghai),
		},
		{
			name:      "monthly day 30 clamps in leap February",
			now:       time.Date(2028, 3, 1, 12, 0, 0, 0, shanghai),
			config:    ClientTrafficConfig{ResetMode: TrafficResetMonthly, Weekday: 1, Day: 30, ResetTime: "23:59"},
			wantStart: time.Date(2028, 2, 29, 23, 59, 0, 0, shanghai),
			wantNext:  time.Date(2028, 3, 30, 23, 59, 0, 0, shanghai),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, err := currentClientCycleStart(
				test.now, test.config.ResetMode, test.config.Weekday, test.config.Day, test.config.ResetTime,
			)
			if err != nil || !start.Equal(test.wantStart) {
				t.Fatalf("current boundary = (%s, %v), want %s", start, err, test.wantStart)
			}
			next, err := nextClientResetAt(
				test.now, test.config.ResetMode, test.config.Weekday, test.config.Day, test.config.ResetTime,
			)
			if err != nil || next == nil || !next.Equal(test.wantNext) {
				t.Fatalf("next boundary = (%v, %v), want %s", next, err, test.wantNext)
			}
		})
	}
	if next, err := nextClientResetAt(time.Now(), TrafficResetNever, 1, 1, "00:00"); err != nil || next != nil {
		t.Fatalf("never next reset = (%v, %v), want nil", next, err)
	}
}

func TestClientTrafficScheduledResetAndManualResetPreserveBaselines(t *testing.T) {
	_, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Resets")
	clientID := proxyValue.Clients[0].ID
	limit := int64(100) << 30
	traffic := ClientTrafficConfig{
		LimitBytes: &limit, ResetMode: TrafficResetDaily, Weekday: 1, Day: 1, ResetTime: "00:00",
	}
	if _, _, err := service.UpdateClient(t.Context(), clientID, ClientUpdateInput{Traffic: &traffic}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 12, 15, 50, 0, 0, time.UTC) // 23:50 in Shanghai.
	service.now = func() time.Time { return now }
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100, DownlinkBytes: 200}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 150, DownlinkBytes: 260}}); err != nil {
		t.Fatal(err)
	}
	metrics := readClientMetrics(t, service, clientID)
	if metrics.CycleUplinkBytes != 50 || metrics.CycleDownlinkBytes != 60 {
		t.Fatalf("pre-boundary metrics = %+v", metrics)
	}

	now = time.Date(2026, 9, 12, 16, 5, 0, 0, time.UTC) // 00:05 the next day in Shanghai.
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 180, DownlinkBytes: 300}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.XrayUplinkBytes != 180 || metrics.XrayDownlinkBytes != 300 ||
		metrics.CycleUplinkBytes != 0 || metrics.CycleDownlinkBytes != 0 ||
		!metrics.CycleStartedAt.Equal(time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("scheduled reset metrics = %+v", metrics)
	}

	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 190, DownlinkBytes: 320}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetClientTraffic(t.Context(), clientID); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.XrayUplinkBytes != 190 || metrics.XrayDownlinkBytes != 320 ||
		metrics.CycleUplinkBytes != 0 || metrics.CycleDownlinkBytes != 0 || !metrics.CycleStartedAt.Equal(now) {
		t.Fatalf("manual reset metrics = %+v", metrics)
	}

	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 197, DownlinkBytes: 331}}); err != nil {
		t.Fatal(err)
	}
	metrics = readClientMetrics(t, service, clientID)
	if metrics.CycleUplinkBytes != 7 || metrics.CycleDownlinkBytes != 11 {
		t.Fatalf("post-manual-reset delta = %+v", metrics)
	}
}

func TestUpdatingClientTrafficConfigDoesNotClearMetrics(t *testing.T) {
	_, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Config update")
	clientID := proxyValue.Clients[0].ID
	now := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100, DownlinkBytes: 200}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := service.RecordClientTraffic(t.Context(), serverID, []ClientTrafficReport{{ClientID: clientID, UplinkBytes: 130, DownlinkBytes: 240}}); err != nil {
		t.Fatal(err)
	}
	before := readClientMetrics(t, service, clientID)
	limit := int64(2) << 40
	traffic := ClientTrafficConfig{
		LimitBytes: &limit, ResetMode: TrafficResetWeekly, Weekday: 7, Day: 1, ResetTime: "03:30",
	}
	updated, _, err := service.UpdateClient(t.Context(), clientID, ClientUpdateInput{Traffic: &traffic})
	if err != nil {
		t.Fatal(err)
	}
	after := readClientMetrics(t, service, clientID)
	if after.CycleUplinkBytes != before.CycleUplinkBytes || after.CycleDownlinkBytes != before.CycleDownlinkBytes ||
		after.XrayUplinkBytes != before.XrayUplinkBytes || after.XrayDownlinkBytes != before.XrayDownlinkBytes ||
		updated.TrafficLimitBytes == nil || *updated.TrafficLimitBytes != limit ||
		updated.TrafficResetMode != TrafficResetWeekly || updated.TrafficResetWeekday != 7 || updated.TrafficResetTime != "03:30" {
		t.Fatalf("updated client = %+v, metrics before/after = %+v / %+v", updated, before, after)
	}
}

func desiredClientStatsID(t *testing.T, db *sql.DB, serverID, clientID int64) string {
	t.Helper()
	values, err := ListDesired(t.Context(), db, serverID)
	if err != nil {
		t.Fatal(err)
	}
	for _, proxyValue := range values {
		for _, client := range proxyValue.Clients {
			if client.ID == clientID {
				return client.StatsID
			}
		}
	}
	t.Fatal("desired client not found")
	return ""
}

func readClientMetrics(t *testing.T, service *Service, clientID int64) *ClientMetrics {
	t.Helper()
	client, err := service.GetClient(t.Context(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	if client.Metrics == nil {
		t.Fatal("client metrics are missing")
	}
	return client.Metrics
}
