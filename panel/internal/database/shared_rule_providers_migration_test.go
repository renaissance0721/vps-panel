package database

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
	"gopkg.in/yaml.v3"
)

func TestSharedTextProvidersMigrationPreservesRoutingAndReferences(t *testing.T) {
	for _, editedDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "original default", true: "edited default"}[editedDefault], func(t *testing.T) {
			dir := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := configure(db); err != nil {
				t.Fatal(err)
			}
			for _, item := range migrations[:19] {
				if err := applyMigration(t.Context(), db, item); err != nil {
					t.Fatal(err)
				}
			}
			var historicalProviders map[string]map[string]any
			if err := yaml.Unmarshal([]byte(defaultRoutingProvidersYAML), &historicalProviders); err != nil {
				t.Fatal(err)
			}
			delete(historicalProviders, "Lan")
			delete(historicalProviders, "ChinaDomain")
			historicalYAML, err := yaml.Marshal(historicalProviders)
			if err != nil {
				t.Fatal(err)
			}
			legacy := strings.NewReplacer("format: text", "format: yaml", "/rule/Surge/", "/rule/Clash/", ".list", ".yaml").Replace(string(historicalYAML))
			legacy = strings.NewReplacer("Netflix/Netflix.yaml", "Netflix/Netflix_Classical.yaml", "Apple/Apple.yaml", "Apple/Apple_Classical.yaml").Replace(legacy)
			const custom = "Custom:\n  type: http\n  behavior: classical\n  format: yaml\n  interval: 86400\n  url: https://example.com/custom.yaml\n"
			if editedDefault {
				legacy = strings.Replace(legacy, "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/OpenAI/OpenAI.yaml", "https://example.com/edited.yaml", 1) + "\n" + custom
			}
			var historicalRules []string
			if err := json.Unmarshal([]byte(defaultRoutingRulesJSON), &historicalRules); err != nil {
				t.Fatal(err)
			}
			filteredRules := historicalRules[:0]
			for _, rule := range historicalRules {
				if rule == "RULE-SET,Lan,DIRECT" || rule == "RULE-SET,ChinaDomain,DIRECT" {
					continue
				}
				if rule == "GEOIP,CN,DIRECT" {
					rule = "GEOIP,CN,DIRECT,no-resolve"
				}
				filteredRules = append(filteredRules, rule)
			}
			historicalRulesJSON, err := json.Marshal(filteredRules)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE subscription_routing_presets SET rule_providers_yaml = ?, rules_json = ? WHERE is_default = 1`, legacy, string(historicalRulesJSON)); err != nil {
				t.Fatal(err)
			}
			// A custom preset even containing old built-in URLs must remain byte-for-byte intact.
			customSource := legacy
			if !editedDefault {
				customSource += "\n" + custom
			}
			if _, err := db.Exec(`INSERT INTO subscription_routing_presets (name, enabled, groups_json, rules_json, rule_providers_yaml, created_at, updated_at)
				SELECT 'custom', 1, groups_json, rules_json, ? ,123,456 FROM subscription_routing_presets WHERE is_default = 1`, customSource); err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1,'admin','test-hash','admin',1,1)`,
				`INSERT INTO subscription_plans (name,enabled,routing_preset_id,routing_bindings_json,created_at,updated_at)
				 SELECT 'plan',1,id,'{"grp_fixture":[7,2]}',123,456 FROM subscription_routing_presets WHERE is_default = 1`,
				`INSERT INTO personal_subscription_groups (owner_user_id,name,token,client_name,routing_preset_id,routing_bindings_json,created_at,updated_at)
				 SELECT 1,'personal','fixture-token','client',id,'{"grp_fixture":[9,3]}',123,456 FROM subscription_routing_presets WHERE is_default = 1`,
			} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			plans := snapshotUserMigrationTable(t, db, "subscription_plans")
			personal := snapshotUserMigrationTable(t, db, "personal_subscription_groups")
			var id int64
			var unchanged string
			const unchangedQuery = `SELECT json_array(id,name,enabled,is_default,groups_json,created_at,updated_at) FROM subscription_routing_presets WHERE is_default = 1`
			if err := db.QueryRow(unchangedQuery).Scan(&unchanged); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT id FROM subscription_routing_presets WHERE is_default = 1`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			assertLatestMigrationHistory(t, db)
			assertForeignKeysValid(t, db)
			var got, source string
			if err := db.QueryRow(unchangedQuery).Scan(&got); err != nil || got != unchanged {
				t.Fatal("default routing identity or logic changed", err)
			}
			if !reflect.DeepEqual(plans, snapshotUserMigrationTable(t, db, "subscription_plans")) || !reflect.DeepEqual(personal, snapshotUserMigrationTable(t, db, "personal_subscription_groups")) {
				t.Fatal("subscription references or bindings changed")
			}
			if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE is_default = 0`).Scan(&source); err != nil || source != customSource {
				t.Fatal("custom preset changed", err)
			}
			if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE id = ?`, id).Scan(&source); err != nil {
				t.Fatal(err)
			}
			var actual, expected map[string]map[string]any
			if err := yaml.Unmarshal([]byte(source), &actual); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal([]byte(defaultRoutingProvidersYAML), &expected); err != nil {
				t.Fatal(err)
			}
			if editedDefault {
				expected["OpenAI"]["url"] = "https://example.com/edited.yaml"
				expected["OpenAI"]["format"] = "yaml"
				var extra map[string]map[string]any
				if err := yaml.Unmarshal([]byte(custom), &extra); err != nil {
					t.Fatal(err)
				}
				expected["Custom"] = extra["Custom"]
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("upgraded providers differ from expected seed or custom values")
			}
			preset, err := subscriptionstore.NewService(db, nil).GetRoutingPreset(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			// Rendering runs the existing complete RoutingPreset validation.
			if _, err := subscriptionstore.RenderMihomoSubscription(subscriptionstore.SubscriptionData{RoutingPreset: &preset}); err != nil {
				t.Fatal(err)
			}
			if !editedDefault {
				if _, err := subscriptionstore.RenderShadowrocketSubscription(subscriptionstore.SubscriptionData{RoutingPreset: &preset}); err != nil {
					t.Fatal(err)
				}
			}
			beforeReopen := snapshotUserMigrationTable(t, db, "subscription_routing_presets")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeReopen, snapshotUserMigrationTable(t, db, "subscription_routing_presets")) {
				t.Fatal("reopen reapplied migration")
			}
			assertLatestMigrationHistory(t, db)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM subscription_routing_presets WHERE is_default = 1`).Scan(&count); err != nil || count != 1 {
				t.Fatal("default duplicated", err)
			}
		})
	}
}

func TestFreshDatabaseUsesSharedTextProviders(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	values, err := subscriptionstore.NewService(db, nil).ListRoutingPresets(t.Context())
	if err != nil || len(values) != 1 || !values[0].IsDefault {
		t.Fatal("missing unique default", err)
	}
	providers := map[string]subscriptionstore.RoutingRuleProvider{}
	for _, provider := range values[0].RuleProviders {
		providers[provider.Name] = provider
	}
	names := []string{"OpenAI", "Claude", "Gemini", "YouTube", "Netflix", "Telegram", "TikTok", "Apple", "Copilot", "Microsoft"}
	if len(providers) != len(names)+2 {
		t.Fatal("unexpected providers")
	}
	for _, name := range names {
		provider := providers[name]
		if provider.Type != "http" || provider.Behavior != "classical" || provider.Format != "text" || provider.Interval != 86400 ||
			provider.URL != "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/"+name+"/"+name+".list" {
			t.Errorf("wrong fresh default %s: %+v", name, provider)
		}
	}
	lan := providers["Lan"]
	if lan.Type != "http" || lan.Behavior != "classical" || lan.Format != "text" || lan.URL != "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/Lan/Lan.list" {
		t.Fatalf("wrong Lan provider: %+v", lan)
	}
	china := providers["ChinaDomain"]
	if china.Type != "http" || china.Behavior != "domain" || china.Format != "text" || china.URL != "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/cn.list" {
		t.Fatalf("wrong ChinaDomain provider: %+v", china)
	}
}
