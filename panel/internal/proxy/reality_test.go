package proxy

import (
	"encoding/base64"
	"testing"
)

func TestCreateRealityProxyGeneratesCompatibleSecrets(t *testing.T) {
	_, service, serverID := newTestService(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "Reality", ListenPort: 8443, EntryHostMode: EntryHostAuto, Enabled: true,
		Security: SecurityReality, ServerName: "www.example.com", RealityTarget: "www.example.com:443",
		FirstClientName: "Phone",
	})
	if err != nil {
		t.Fatalf("create REALITY proxy: %v", err)
	}
	_, config, err := getProxyForTest(service, value.ID)
	if err != nil || config.Reality == nil || config.Reality.PublicKey == "" ||
		len(config.Reality.ShortID) != 16 || validateReality(config.Reality) != nil {
		t.Fatalf("stored REALITY config = %+v, %v", config, err)
	}
	privateBytes, _ := base64.RawURLEncoding.DecodeString(config.Reality.PrivateKey)
	if len(privateBytes) != 32 || privateBytes[0]&7 != 0 || privateBytes[31]&128 != 0 || privateBytes[31]&64 == 0 {
		t.Fatalf("private key is not Xray-compatible: length %d", len(privateBytes))
	}
}
