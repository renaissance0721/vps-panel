package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAuditLogRecordsMutationsAndIgnoresReadsAndSecrets(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	created := createAccessTestServer(t, handler, accounts.adminCookie, "Audited", "public", nil)
	createOrderProxy(t, handler, accounts.adminCookie, created.Server.ID, "Audited Proxy", 10443)

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, handler, http.MethodGet,
		"/api/admin/audit-logs?page=1&page_size=1&action=proxy.create&user=admin", nil, accounts.adminCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("list audit logs = %d, %s", response.Code, response.Body.String())
	}
	var data struct {
		AuditLogs []auditLogResponse `json:"audit_logs"`
		Total     int                `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Total != 1 || len(data.AuditLogs) != 1 || data.AuditLogs[0].Action != "proxy.create" ||
		data.AuditLogs[0].ActorUsername != "admin" || data.AuditLogs[0].RequestID == "" {
		t.Fatalf("filtered audit logs = %+v", data)
	}
	if secondRead := performRequest(t, handler, http.MethodGet, "/api/admin/audit-logs", nil, accounts.adminCookie); secondRead.Code != http.StatusOK {
		t.Fatalf("second audit read = %d", secondRead.Code)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("GET created audit rows: before %d, after %d", before, after)
	}
	if forbidden := performRequest(t, handler, http.MethodGet, "/api/admin/audit-logs", nil, accounts.memberCookie); forbidden.Code != http.StatusForbidden {
		t.Fatalf("member audit list = %d", forbidden.Code)
	}

	rows, err := db.Query(`SELECT action || ' ' || summary FROM audit_logs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"strong-password", "another-strong-password", "subscription_token", "private_key", "credential_json"} {
			if strings.Contains(value, secret) {
				t.Fatalf("audit log leaked %q: %q", secret, value)
			}
		}
	}
}
