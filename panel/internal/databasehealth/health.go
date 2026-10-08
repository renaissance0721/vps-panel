package databasehealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const MaxForeignKeyDetails = 10

var ErrManualIntervention = errors.New("manual intervention required")

type ForeignKeyViolation struct {
	Table          string
	RowID          *int64
	ChildColumn    string
	Value          string
	ValueAvailable bool
	ParentTable    string
	ParentColumn   string
	ForeignKeyID   int
	OnDelete       string
	OnUpdate       string
}

type ForeignKeyResult struct {
	Total      int
	Violations []ForeignKeyViolation
}

type Check struct {
	Category string
	Name     string
	Count    int
}

type Report struct {
	IntegrityMessages []string
	ForeignKeys       ForeignKeyResult
	Checks            []Check
}

func (r Report) IntegrityOK() bool {
	return len(r.IntegrityMessages) == 1 && r.IntegrityMessages[0] == "ok"
}

func (r Report) Healthy() bool {
	if !r.IntegrityOK() || r.ForeignKeys.Total != 0 {
		return false
	}
	for _, check := range r.Checks {
		if check.Count != 0 {
			return false
		}
	}
	return true
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func CheckPath(ctx context.Context, path string) (Report, error) {
	db, err := openSQLite(ctx, path, true)
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	return inspectDB(ctx, db)
}

func Inspect(ctx context.Context, query queryer) (Report, error) {
	integrity, err := IntegrityCheck(ctx, query)
	if err != nil {
		return Report{}, err
	}
	foreignKeys, err := ForeignKeyCheck(ctx, query, MaxForeignKeyDetails)
	if err != nil {
		return Report{}, err
	}
	schema, err := readSchema(ctx, query)
	if err != nil {
		return Report{}, err
	}
	checks := make([]Check, 0, len(healthCheckSpecs))
	for _, spec := range healthCheckSpecs {
		if !schemaSupports(schema, spec.Required) {
			continue
		}
		var count int
		if err := query.QueryRowContext(ctx, spec.SQL).Scan(&count); err != nil {
			return Report{}, fmt.Errorf("run database health check %q: %w", spec.Name, err)
		}
		checks = append(checks, Check{Category: spec.Category, Name: spec.Name, Count: count})
	}
	return Report{IntegrityMessages: integrity, ForeignKeys: foreignKeys, Checks: checks}, nil
}

func IntegrityCheck(ctx context.Context, query queryer) ([]string, error) {
	rows, err := query.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return nil, fmt.Errorf("run SQLite integrity_check: %w", err)
	}
	defer rows.Close()
	results := make([]string, 0, 1)
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return nil, fmt.Errorf("scan SQLite integrity_check: %w", err)
		}
		if len(results) < MaxForeignKeyDetails {
			results = append(results, result)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite integrity_check: %w", err)
	}
	return results, nil
}

func ForeignKeyCheck(ctx context.Context, query queryer, detailLimit int) (ForeignKeyResult, error) {
	if detailLimit < 0 {
		detailLimit = 0
	}
	type rawViolation struct {
		table  string
		rowID  sql.NullInt64
		parent string
		fkID   int
	}
	rows, err := query.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return ForeignKeyResult{}, fmt.Errorf("run SQLite foreign_key_check: %w", err)
	}
	raw := make([]rawViolation, 0)
	total := 0
	for rows.Next() {
		var value rawViolation
		if err := rows.Scan(&value.table, &value.rowID, &value.parent, &value.fkID); err != nil {
			rows.Close()
			return ForeignKeyResult{}, fmt.Errorf("scan SQLite foreign_key_check: %w", err)
		}
		total++
		if len(raw) < detailLimit {
			raw = append(raw, value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ForeignKeyResult{}, fmt.Errorf("iterate SQLite foreign_key_check: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ForeignKeyResult{}, fmt.Errorf("close SQLite foreign_key_check: %w", err)
	}

	result := ForeignKeyResult{Total: total, Violations: make([]ForeignKeyViolation, 0, len(raw))}
	for _, value := range raw {
		violation := ForeignKeyViolation{
			Table: value.table, ParentTable: value.parent, ForeignKeyID: value.fkID,
		}
		if value.rowID.Valid {
			rowID := value.rowID.Int64
			violation.RowID = &rowID
		}
		populateForeignKeyDetails(ctx, query, &violation)
		result.Violations = append(result.Violations, violation)
	}
	return result, nil
}

func FormatForeignKeyViolations(result ForeignKeyResult) string {
	var output strings.Builder
	fmt.Fprintf(&output, "total violations: %d", result.Total)
	for index, violation := range result.Violations {
		fmt.Fprintf(&output, "\n\nViolation %d:\n  table: %s", index+1, violation.Table)
		if violation.RowID != nil {
			fmt.Fprintf(&output, "\n  rowid: %d", *violation.RowID)
		} else {
			output.WriteString("\n  rowid: unavailable")
		}
		if violation.ChildColumn != "" {
			fmt.Fprintf(&output, "\n  column: %s", violation.ChildColumn)
		}
		if violation.ValueAvailable {
			fmt.Fprintf(&output, "\n  value: %s", violation.Value)
		}
		parent := violation.ParentTable
		if violation.ParentColumn != "" {
			parent += "(" + violation.ParentColumn + ")"
		}
		fmt.Fprintf(&output, "\n  parent: %s", parent)
		if violation.OnDelete != "" {
			fmt.Fprintf(&output, "\n  on_delete: %s", violation.OnDelete)
		}
		if violation.OnUpdate != "" {
			fmt.Fprintf(&output, "\n  on_update: %s", violation.OnUpdate)
		}
	}
	if result.Total > len(result.Violations) {
		fmt.Fprintf(&output, "\n\n%d additional violation(s) omitted", result.Total-len(result.Violations))
	}
	return output.String()
}

type RepairCount struct {
	Table string
	Count int64
}

type RepairResult struct {
	Before       Report
	After        Report
	BackupPath   string
	Repaired     []RepairCount
	Committed    bool
	ManualNeeded bool
}

func Repair(ctx context.Context, path string, now time.Time) (RepairResult, error) {
	db, err := openSQLite(ctx, path, false)
	if err != nil {
		return RepairResult{}, err
	}
	defer db.Close()
	before, err := inspectDB(ctx, db)
	if err != nil {
		return RepairResult{}, err
	}
	result := RepairResult{Before: before}
	result.BackupPath, err = createRepairBackup(ctx, db, path, now)
	if err != nil {
		return result, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin database repair: %w", err)
	}
	defer tx.Rollback()
	schema, err := readSchema(ctx, tx)
	if err != nil {
		return result, err
	}
	for _, spec := range repairSpecs {
		if !schemaSupports(schema, spec.Required) {
			continue
		}
		change, err := tx.ExecContext(ctx, spec.SQL)
		if err != nil {
			return result, fmt.Errorf("repair %s orphan rows: %w", spec.Table, err)
		}
		count, err := change.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("count repaired %s rows: %w", spec.Table, err)
		}
		result.Repaired = append(result.Repaired, RepairCount{Table: spec.Table, Count: count})
	}
	result.After, err = Inspect(ctx, tx)
	if err != nil {
		return result, err
	}
	if !result.After.IntegrityOK() || result.After.ForeignKeys.Total != 0 {
		result.ManualNeeded = true
		return result, fmt.Errorf("database remains unsafe after order cleanup: %w", ErrManualIntervention)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit database repair: %w", err)
	}
	result.Committed = true
	result.After, err = CheckPath(ctx, path)
	if err != nil {
		return result, fmt.Errorf("verify repaired database: %w", err)
	}
	result.ManualNeeded = !result.After.Healthy()
	return result, nil
}

func inspectDB(ctx context.Context, db *sql.DB) (Report, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Report{}, fmt.Errorf("begin database health check: %w", err)
	}
	defer tx.Rollback()
	return Inspect(ctx, tx)
}

func openSQLite(ctx context.Context, path string, queryOnly bool) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("SQLite database is not a regular file")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if queryOnly {
		if _, err := db.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure read-only SQLite check: %w", err)
		}
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	return db, nil
}

func createRepairBackup(ctx context.Context, db *sql.DB, source string, now time.Time) (string, error) {
	base := source + ".repair-backup-" + now.UTC().Format("20060102T150405Z")
	destination := base
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(destination); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", fmt.Errorf("inspect repair backup destination: %w", err)
		}
		destination = fmt.Sprintf("%s-%d", base, suffix)
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, destination); err != nil {
		return "", fmt.Errorf("create consistent repair backup: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("protect repair backup: %w", err)
	}
	backup, err := openSQLite(ctx, destination, true)
	if err != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("validate repair backup: %w", err)
	}
	integrity, integrityErr := IntegrityCheck(ctx, backup)
	closeErr := backup.Close()
	if integrityErr != nil || len(integrity) != 1 || integrity[0] != "ok" {
		_ = os.Remove(destination)
		if integrityErr != nil {
			return "", fmt.Errorf("validate repair backup: %w", integrityErr)
		}
		return "", errors.New("validate repair backup: SQLite integrity_check failed")
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("close repair backup: %w", closeErr)
	}
	return destination, nil
}

type foreignKeyColumn struct {
	sequence     int
	childColumn  string
	parentColumn string
	onDelete     string
	onUpdate     string
}

func populateForeignKeyDetails(ctx context.Context, query queryer, violation *ForeignKeyViolation) {
	rows, err := query.QueryContext(ctx, `PRAGMA foreign_key_list(`+sqliteString(violation.Table)+`)`)
	if err != nil {
		return
	}
	columns := make([]foreignKeyColumn, 0, 1)
	for rows.Next() {
		var id int
		var column foreignKeyColumn
		var parent, match string
		var childColumn, parentColumn sql.NullString
		if rows.Scan(&id, &column.sequence, &parent, &childColumn, &parentColumn,
			&column.onUpdate, &column.onDelete, &match) != nil {
			continue
		}
		if id == violation.ForeignKeyID {
			column.childColumn = childColumn.String
			column.parentColumn = parentColumn.String
			columns = append(columns, column)
			if parent != "" {
				violation.ParentTable = parent
			}
		}
	}
	_ = rows.Close()
	if len(columns) == 0 {
		return
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].sequence < columns[j].sequence })
	children := make([]string, 0, len(columns))
	parents := make([]string, 0, len(columns))
	parentColumnsAvailable := true
	for _, column := range columns {
		children = append(children, column.childColumn)
		if column.parentColumn == "" {
			parentColumnsAvailable = false
		} else {
			parents = append(parents, column.parentColumn)
		}
	}
	violation.ChildColumn = strings.Join(children, ",")
	if parentColumnsAvailable {
		violation.ParentColumn = strings.Join(parents, ",")
	}
	violation.OnDelete = columns[0].onDelete
	violation.OnUpdate = columns[0].onUpdate
	if violation.RowID == nil {
		return
	}
	for _, column := range columns {
		if column.childColumn == "" || sensitiveColumn(column.childColumn) {
			return
		}
	}
	selects := make([]string, 0, len(columns))
	for _, column := range columns {
		selects = append(selects, `CAST(`+sqliteIdentifier(column.childColumn)+` AS TEXT)`)
	}
	values := make([]sql.NullString, len(columns))
	destinations := make([]any, len(columns))
	for index := range values {
		destinations[index] = &values[index]
	}
	statement := `SELECT ` + strings.Join(selects, ",") + ` FROM ` + sqliteIdentifier(violation.Table) + ` WHERE rowid = ?`
	if err := query.QueryRowContext(ctx, statement, *violation.RowID).Scan(destinations...); err != nil {
		return
	}
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		if value.Valid {
			formatted = append(formatted, value.String)
		} else {
			formatted = append(formatted, "NULL")
		}
	}
	violation.Value = strings.Join(formatted, ",")
	violation.ValueAvailable = true
}

func sensitiveColumn(column string) bool {
	value := strings.ToLower(column)
	for _, marker := range []string{"password", "token", "credential", "secret", "private", "hash", "uri"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func sqliteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sqliteString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func readSchema(ctx context.Context, query queryer) (map[string]map[string]struct{}, error) {
	rows, err := query.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list SQLite tables: %w", err)
	}
	tables := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan SQLite table: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate SQLite tables: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite tables: %w", err)
	}
	schema := make(map[string]map[string]struct{}, len(tables))
	for _, table := range tables {
		columnRows, err := query.QueryContext(ctx, `PRAGMA table_info(`+sqliteString(table)+`)`)
		if err != nil {
			return nil, fmt.Errorf("read SQLite table %s: %w", table, err)
		}
		columns := make(map[string]struct{})
		for columnRows.Next() {
			var sequence, notNull, primaryKey int
			var name, kind string
			var defaultValue any
			if err := columnRows.Scan(&sequence, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				columnRows.Close()
				return nil, fmt.Errorf("scan SQLite table %s: %w", table, err)
			}
			columns[name] = struct{}{}
		}
		if err := columnRows.Close(); err != nil {
			return nil, fmt.Errorf("close SQLite table %s: %w", table, err)
		}
		schema[table] = columns
	}
	return schema, nil
}

func schemaSupports(schema map[string]map[string]struct{}, required map[string][]string) bool {
	for table, columns := range required {
		actual, exists := schema[table]
		if !exists {
			return false
		}
		for _, column := range columns {
			if _, exists := actual[column]; !exists {
				return false
			}
		}
	}
	return true
}

type checkSpec struct {
	Category string
	Name     string
	Required map[string][]string
	SQL      string
}

var healthCheckSpecs = []checkSpec{
	{Category: "orphan-order", Name: "user_server_order orphan rows", Required: columns("user_server_order", "user_id", "server_id", "users", "id", "servers", "id"), SQL: `SELECT COUNT(*) FROM user_server_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN servers ON servers.id = value.server_id WHERE users.id IS NULL OR servers.id IS NULL`},
	{Category: "orphan-order", Name: "user_proxy_order orphan rows", Required: columns("user_proxy_order", "user_id", "proxy_id", "users", "id", "proxies", "id"), SQL: `SELECT COUNT(*) FROM user_proxy_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN proxies ON proxies.id = value.proxy_id WHERE users.id IS NULL OR proxies.id IS NULL`},
	{Category: "orphan-order", Name: "user_relay_order orphan rows", Required: columns("user_relay_order", "user_id", "relay_id", "users", "id", "relays", "id"), SQL: `SELECT COUNT(*) FROM user_relay_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN relays ON relays.id = value.relay_id WHERE users.id IS NULL OR relays.id IS NULL`},
	{Category: "orphan-order", Name: "user_landing_order orphan rows", Required: columns("user_landing_order", "user_id", "landing_id", "users", "id", "landing_nodes", "id"), SQL: `SELECT COUNT(*) FROM user_landing_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN landing_nodes ON landing_nodes.id = value.landing_id WHERE users.id IS NULL OR landing_nodes.id IS NULL`},
	{Category: "orphan-order", Name: "user_account_order orphan rows", Required: columns("user_account_order", "user_id", "account_user_id", "users", "id"), SQL: `SELECT COUNT(*) FROM user_account_order AS value LEFT JOIN users AS owner ON owner.id = value.user_id LEFT JOIN users AS account ON account.id = value.account_user_id WHERE owner.id IS NULL OR account.id IS NULL`},
	{Category: "orphan-order", Name: "user_personal_subscription_order orphan rows", Required: columns("user_personal_subscription_order", "user_id", "personal_subscription_id", "users", "id", "personal_subscription_groups", "id"), SQL: `SELECT COUNT(*) FROM user_personal_subscription_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN personal_subscription_groups ON personal_subscription_groups.id = value.personal_subscription_id WHERE users.id IS NULL OR personal_subscription_groups.id IS NULL`},
	{Category: "orphan-order", Name: "user_published_node_order orphan rows", Required: columns("user_published_node_order", "user_id", "published_node_id", "users", "id", "subscription_published_nodes", "id"), SQL: `SELECT COUNT(*) FROM user_published_node_order AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN subscription_published_nodes ON subscription_published_nodes.id = value.published_node_id WHERE users.id IS NULL OR subscription_published_nodes.id IS NULL`},
	{Category: "relay", Name: "enabled Relay references archived target server", Required: columns("relays", "enabled", "target_type", "target_proxy_id", "proxies", "id", "server_id", "servers", "id", "archived_at"), SQL: `SELECT COUNT(*) FROM relays JOIN proxies ON proxies.id = relays.target_proxy_id JOIN servers ON servers.id = proxies.server_id WHERE relays.enabled = 1 AND relays.target_type = 'proxy' AND servers.archived_at IS NOT NULL`},
	{Category: "relay", Name: "enabled Relay target Proxy does not exist", Required: columns("relays", "enabled", "target_type", "target_proxy_id", "proxies", "id"), SQL: `SELECT COUNT(*) FROM relays LEFT JOIN proxies ON proxies.id = relays.target_proxy_id WHERE relays.enabled = 1 AND relays.target_type = 'proxy' AND proxies.id IS NULL`},
	{Category: "relay", Name: "enabled Relay target server does not exist", Required: columns("relays", "enabled", "target_type", "target_proxy_id", "proxies", "id", "server_id", "servers", "id"), SQL: `SELECT COUNT(*) FROM relays JOIN proxies ON proxies.id = relays.target_proxy_id LEFT JOIN servers ON servers.id = proxies.server_id WHERE relays.enabled = 1 AND relays.target_type = 'proxy' AND servers.id IS NULL`},
	{Category: "archive", Name: "archived server still has enabled Proxy", Required: columns("servers", "id", "archived_at", "proxies", "server_id", "enabled"), SQL: `SELECT COUNT(*) FROM proxies JOIN servers ON servers.id = proxies.server_id WHERE proxies.enabled = 1 AND servers.archived_at IS NOT NULL`},
	{Category: "archive", Name: "archived server still has enabled Relay", Required: columns("servers", "id", "archived_at", "relays", "server_id", "enabled"), SQL: `SELECT COUNT(*) FROM relays JOIN servers ON servers.id = relays.server_id WHERE relays.enabled = 1 AND servers.archived_at IS NOT NULL`},
	{Category: "subscription", Name: "subscription_published_nodes invalid references", Required: columns("subscription_published_nodes", "mode", "target_proxy_id", "source_server_id", "relay_id", "proxies", "id", "servers", "id", "relays", "id"), SQL: `SELECT COUNT(*) FROM subscription_published_nodes AS value LEFT JOIN proxies ON proxies.id = value.target_proxy_id LEFT JOIN servers ON servers.id = value.source_server_id LEFT JOIN relays ON relays.id = value.relay_id WHERE proxies.id IS NULL OR (value.source_server_id IS NOT NULL AND servers.id IS NULL) OR (value.relay_id IS NOT NULL AND relays.id IS NULL) OR (value.mode = 'direct' AND (value.source_server_id IS NOT NULL OR value.relay_id IS NOT NULL)) OR (value.mode = 'relay' AND (value.source_server_id IS NULL OR value.relay_id IS NULL))`},
	{Category: "subscription", Name: "personal_subscription_nodes invalid source", Required: columns("personal_subscription_nodes", "source_type", "source_id", "entry_host", "proxies", "id", "relays", "id", "landing_nodes", "id"), SQL: `SELECT COUNT(*) FROM personal_subscription_nodes AS value LEFT JOIN proxies ON value.source_type = 'proxy' AND proxies.id = value.source_id LEFT JOIN relays ON value.source_type = 'relay' AND relays.id = value.source_id LEFT JOIN landing_nodes ON value.source_type = 'landing' AND landing_nodes.id = value.source_id WHERE (value.source_type = 'proxy' AND proxies.id IS NULL) OR (value.source_type = 'relay' AND relays.id IS NULL) OR (value.source_type = 'landing' AND landing_nodes.id IS NULL) OR value.source_type NOT IN ('proxy','relay','landing')`},
	{Category: "subscriber", Name: "subscriber_clients invalid references", Required: columns("subscriber_clients", "user_id", "proxy_id", "client_id", "users", "id", "proxies", "id", "clients", "id", "proxy_id", "assigned_user_id"), SQL: `SELECT COUNT(*) FROM subscriber_clients AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN proxies ON proxies.id = value.proxy_id LEFT JOIN clients ON clients.id = value.client_id WHERE users.id IS NULL OR proxies.id IS NULL OR clients.id IS NULL OR clients.proxy_id != value.proxy_id OR clients.assigned_user_id IS NULL OR clients.assigned_user_id != value.user_id`},
	{Category: "subscriber", Name: "subscriber_profiles invalid references", Required: columns("subscriber_profiles", "user_id", "plan_id", "users", "id", "subscription_plans", "id"), SQL: `SELECT COUNT(*) FROM subscriber_profiles AS value LEFT JOIN users ON users.id = value.user_id LEFT JOIN subscription_plans ON subscription_plans.id = value.plan_id WHERE users.id IS NULL OR (value.plan_id IS NOT NULL AND subscription_plans.id IS NULL)`},
	{Category: "subscriber", Name: "subscriber_usage invalid references", Required: columns("subscriber_usage", "user_id", "users", "id"), SQL: `SELECT COUNT(*) FROM subscriber_usage AS value LEFT JOIN users ON users.id = value.user_id WHERE users.id IS NULL`},
	{Category: "subscription", Name: "subscription_plan_nodes invalid references", Required: columns("subscription_plan_nodes", "plan_id", "published_node_id", "subscription_plans", "id", "subscription_published_nodes", "id"), SQL: `SELECT COUNT(*) FROM subscription_plan_nodes AS value LEFT JOIN subscription_plans ON subscription_plans.id = value.plan_id LEFT JOIN subscription_published_nodes ON subscription_published_nodes.id = value.published_node_id WHERE subscription_plans.id IS NULL OR subscription_published_nodes.id IS NULL`},
}

type repairSpec struct {
	Table    string
	Required map[string][]string
	SQL      string
}

var repairSpecs = []repairSpec{
	{Table: "user_server_order", Required: columns("user_server_order", "user_id", "server_id", "users", "id", "servers", "id"), SQL: `DELETE FROM user_server_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_server_order.user_id) OR NOT EXISTS (SELECT 1 FROM servers WHERE servers.id = user_server_order.server_id)`},
	{Table: "user_proxy_order", Required: columns("user_proxy_order", "user_id", "proxy_id", "users", "id", "proxies", "id"), SQL: `DELETE FROM user_proxy_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_proxy_order.user_id) OR NOT EXISTS (SELECT 1 FROM proxies WHERE proxies.id = user_proxy_order.proxy_id)`},
	{Table: "user_relay_order", Required: columns("user_relay_order", "user_id", "relay_id", "users", "id", "relays", "id"), SQL: `DELETE FROM user_relay_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_relay_order.user_id) OR NOT EXISTS (SELECT 1 FROM relays WHERE relays.id = user_relay_order.relay_id)`},
	{Table: "user_landing_order", Required: columns("user_landing_order", "user_id", "landing_id", "users", "id", "landing_nodes", "id"), SQL: `DELETE FROM user_landing_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_landing_order.user_id) OR NOT EXISTS (SELECT 1 FROM landing_nodes WHERE landing_nodes.id = user_landing_order.landing_id)`},
	{Table: "user_account_order", Required: columns("user_account_order", "user_id", "account_user_id", "users", "id"), SQL: `DELETE FROM user_account_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_account_order.user_id) OR NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_account_order.account_user_id)`},
	{Table: "user_personal_subscription_order", Required: columns("user_personal_subscription_order", "user_id", "personal_subscription_id", "users", "id", "personal_subscription_groups", "id"), SQL: `DELETE FROM user_personal_subscription_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_personal_subscription_order.user_id) OR NOT EXISTS (SELECT 1 FROM personal_subscription_groups WHERE personal_subscription_groups.id = user_personal_subscription_order.personal_subscription_id)`},
	{Table: "user_published_node_order", Required: columns("user_published_node_order", "user_id", "published_node_id", "users", "id", "subscription_published_nodes", "id"), SQL: `DELETE FROM user_published_node_order WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = user_published_node_order.user_id) OR NOT EXISTS (SELECT 1 FROM subscription_published_nodes WHERE subscription_published_nodes.id = user_published_node_order.published_node_id)`},
}

func columns(values ...string) map[string][]string {
	result := make(map[string][]string)
	for index := 0; index < len(values); {
		table := values[index]
		index++
		for index < len(values) {
			value := values[index]
			if knownTable(value) {
				break
			}
			result[table] = append(result[table], value)
			index++
		}
	}
	return result
}

func knownTable(value string) bool {
	for _, table := range []string{
		"users", "servers", "proxies", "relays", "landing_nodes",
		"user_server_order", "user_proxy_order", "user_relay_order", "user_landing_order", "user_account_order",
		"user_personal_subscription_order", "user_published_node_order",
		"subscription_published_nodes", "personal_subscription_groups", "personal_subscription_nodes", "subscriber_clients", "subscriber_profiles",
		"subscriber_usage", "subscription_plans", "subscription_plan_nodes", "clients",
	} {
		if value == table {
			return true
		}
	}
	return false
}

func DatabasePath(dataDir string) string {
	return filepath.Join(dataDir, "panel.db")
}
