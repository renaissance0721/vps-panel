package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestToServerResponseIncludesCountryCode(t *testing.T) {
	for _, code := range []string{"DE", "TW", "HK", "MO"} {
		response := toServerResponse(serverstore.Server{
			SystemInfo: &serverstore.SystemInfo{CountryCode: code},
		}, "dev")
		if response.SystemInfo == nil || response.SystemInfo.CountryCode != code {
			t.Fatalf("country_code for %s = %+v", code, response.SystemInfo)
		}
	}
}

type apiGeoIPProviderFunc func(context.Context, string) (string, error)

func (f apiGeoIPProviderFunc) CountryCode(ctx context.Context, ip string) (string, error) {
	return f(ctx, ip)
}

func TestAgentGeoIPTaiwanIsReturnedByServerAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Taiwan Server")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	provider := apiGeoIPProviderFunc(func(context.Context, string) (string, error) { return "tw", nil })
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{}, provider)
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d", initialized.Code)
	}
	cookie := initialized.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + agent.Token}}
	connection, _, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{"type":"system_info","public_ipv4":"198.51.100.20"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		response := performRequest(t, handler, http.MethodGet, "/api/servers/"+strconv.FormatInt(created.ID, 10), nil, cookie)
		var value struct {
			Server serverResponse `json:"server"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &value) != nil {
			t.Fatal("read server API failed")
		}
		if value.Server.SystemInfo != nil && value.Server.SystemInfo.CountryCode == "TW" {
			if value.Server.Name != "Taiwan Server" {
				t.Fatal("GeoIP changed server name")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GeoIP tw was not returned as country_code TW")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
