package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestImproveDefaultCNRoutingPreservesCustomRulesAndPresets(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:22] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			t.Fatal(err)
		}
	}

	var providers map[string]map[string]any
	if err := yaml.Unmarshal([]byte(defaultRoutingProvidersYAML), &providers); err != nil {
		t.Fatal(err)
	}
	delete(providers, "Lan")
	delete(providers, "ChinaDomain")
	providers["PayPal"] = map[string]any{
		"type": "http", "behavior": "classical", "format": "text", "interval": 43200,
		"url": "https://example.com/paypal.list",
	}
	legacyProviders, err := yaml.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	legacyRules := []string{
		"RULE-SET,OpenAI,🤖 AI",
		"RULE-SET,PayPal,🚀 默认代理",
		"GEOIP,CN,DIRECT,no-resolve",
		"MATCH,🚀 默认代理",
	}
	legacyRulesJSON, _ := json.Marshal(legacyRules)
	if _, err := db.Exec(`UPDATE subscription_routing_presets
		SET rule_providers_yaml = ?, rules_json = ? WHERE is_default = 1`, string(legacyProviders), string(legacyRulesJSON)); err != nil {
		t.Fatal(err)
	}
	const customProviders = "Custom:\n  type: http\n  behavior: classical\n  format: text\n  interval: 3600\n  url: https://example.com/custom.list"
	const customRules = `["RULE-SET,Custom,DIRECT","MATCH,DIRECT"]`
	if _, err := db.Exec(`INSERT INTO subscription_routing_presets
		(name, enabled, is_default, groups_json, rule_providers_yaml, rules_json, created_at, updated_at)
		VALUES ('Custom', 1, 0, ?, ?, ?, 1, 1)`, defaultRoutingGroupsJSON, customProviders, customRules); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(context.Background(), db, migrations[22]); err != nil {
		t.Fatal(err)
	}

	var upgradedProviders, upgradedRulesJSON string
	if err := db.QueryRow(`SELECT rule_providers_yaml, rules_json FROM subscription_routing_presets WHERE is_default = 1`).
		Scan(&upgradedProviders, &upgradedRulesJSON); err != nil {
		t.Fatal(err)
	}
	var upgraded map[string]map[string]any
	if err := yaml.Unmarshal([]byte(upgradedProviders), &upgraded); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Lan", "ChinaDomain", "PayPal"} {
		if upgraded[name] == nil {
			t.Fatalf("missing provider %s", name)
		}
	}
	if upgraded["PayPal"]["url"] != "https://example.com/paypal.list" || upgraded["PayPal"]["interval"] != 43200 {
		t.Fatalf("custom provider changed: %#v", upgraded["PayPal"])
	}
	var upgradedRules []string
	if err := json.Unmarshal([]byte(upgradedRulesJSON), &upgradedRules); err != nil {
		t.Fatal(err)
	}
	wantRules := []string{
		"RULE-SET,Lan,DIRECT",
		"RULE-SET,OpenAI,🤖 AI",
		"RULE-SET,PayPal,🚀 默认代理",
		"RULE-SET,ChinaDomain,DIRECT",
		"GEOIP,CN,DIRECT",
		"MATCH,🚀 默认代理",
	}
	if !reflect.DeepEqual(upgradedRules, wantRules) {
		t.Fatalf("upgraded rules = %#v, want %#v", upgradedRules, wantRules)
	}
	var untouchedProviders, untouchedRules string
	if err := db.QueryRow(`SELECT rule_providers_yaml, rules_json FROM subscription_routing_presets WHERE is_default = 0`).
		Scan(&untouchedProviders, &untouchedRules); err != nil || untouchedProviders != customProviders || untouchedRules != customRules {
		t.Fatalf("custom preset changed: %q / %q, %v", untouchedProviders, untouchedRules, err)
	}
}
