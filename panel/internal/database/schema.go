package database

// schemaStatements defines the current schema without altering migration order.
func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'vip')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_invitations_active
			ON admin_invitations(used_at, expires_at)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			visibility TEXT NOT NULL DEFAULT 'public'
				CHECK (visibility IN ('public', 'private')),
			outbound_preference TEXT NOT NULL DEFAULT 'auto'
				CHECK (outbound_preference IN ('auto', 'prefer_ipv4', 'prefer_ipv6')),
			block_china_inbound INTEGER NOT NULL DEFAULT 0
				CHECK (block_china_inbound IN (0, 1)),
			desired_state_version INTEGER NOT NULL DEFAULT 1,
			archived_at INTEGER,
			expires_at INTEGER,
			monthly_traffic_limit_bytes INTEGER CHECK (monthly_traffic_limit_bytes >= 0),
			traffic_count_mode TEXT NOT NULL DEFAULT 'single'
				CHECK (traffic_count_mode IN ('single', 'bidirectional')),
			traffic_reset_day INTEGER NOT NULL DEFAULT 1
				CHECK (traffic_reset_day BETWEEN 1 AND 31),
			traffic_reset_time TEXT NOT NULL DEFAULT '00:00',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_access (
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (server_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_server_access_server_id ON server_access(server_id)`,
		`CREATE INDEX IF NOT EXISTS idx_server_access_user_id ON server_access(user_id)`,
		`CREATE TABLE IF NOT EXISTS user_server_order (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			PRIMARY KEY (user_id, server_id),
			UNIQUE (user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS agent_enrollments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			purpose TEXT NOT NULL DEFAULT 'initial' CHECK (purpose IN ('initial', 'rebind')),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_enrollments_server_id
			ON agent_enrollments(server_id)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL UNIQUE REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			implementation TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL,
			api_version INTEGER NOT NULL DEFAULT 0,
			capabilities_json TEXT NOT NULL DEFAULT '[]',
			registered_at INTEGER NOT NULL,
			last_seen_at INTEGER,
			applied_config_version INTEGER NOT NULL DEFAULT 0,
			config_sync_status TEXT NOT NULL DEFAULT 'pending'
				CHECK (config_sync_status IN ('pending', 'success', 'failed')),
			config_sync_error TEXT NOT NULL DEFAULT '',
			config_synced_at INTEGER,
			upgrade_target_version TEXT NOT NULL DEFAULT '',
			upgrade_status TEXT NOT NULL DEFAULT ''
				CHECK (upgrade_status IN ('', 'upgrading', 'failed')),
			upgrade_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_system_info (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			hostname TEXT NOT NULL,
			os_name TEXT NOT NULL,
			os_version TEXT NOT NULL,
			kernel TEXT NOT NULL,
			arch TEXT NOT NULL,
			ipv4 TEXT NOT NULL,
			ipv6 TEXT NOT NULL,
			public_ipv4 TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL,
			reported_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_metrics (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			cpu_percent REAL NOT NULL,
			memory_used_bytes INTEGER NOT NULL,
			memory_total_bytes INTEGER NOT NULL,
			disk_used_bytes INTEGER NOT NULL,
			disk_total_bytes INTEGER NOT NULL,
			uptime_seconds INTEGER NOT NULL,
			nic_rx_bytes INTEGER NOT NULL DEFAULT 0,
			nic_tx_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_rx_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_tx_bytes INTEGER NOT NULL DEFAULT 0,
			traffic_adjustment_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_started_at INTEGER,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'shadowsocks')),
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto'
				CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (server_id, listen_port)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proxies_server_id ON proxies(server_id)`,
		`CREATE TABLE IF NOT EXISTS user_proxy_order (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			PRIMARY KEY (user_id, proxy_id),
			UNIQUE (user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS landing_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_user_id INTEGER NOT NULL REFERENCES users(id),
			name TEXT NOT NULL,
			visibility TEXT NOT NULL DEFAULT 'private'
				CHECK (visibility IN ('private', 'public')),
			protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'shadowsocks')),
			host TEXT NOT NULL,
			port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
			uri TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_landing_nodes_owner_user_id ON landing_nodes(owner_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_landing_nodes_visibility ON landing_nodes(visibility)`,
		`CREATE TABLE IF NOT EXISTS relays (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			listen_address TEXT NOT NULL DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto'
				CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL CHECK (target_type IN ('proxy', 'landing', 'manual')),
			target_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
			target_client_id INTEGER NULL REFERENCES clients(id) ON DELETE SET NULL,
			target_landing_id INTEGER NULL REFERENCES landing_nodes(id) ON DELETE RESTRICT,
			target_host TEXT NOT NULL DEFAULT '',
			target_port INTEGER CHECK (target_port BETWEEN 1 AND 65535),
			network TEXT NOT NULL CHECK (network IN ('tcp', 'udp', 'tcp,udp')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (
				(target_type = 'proxy' AND target_proxy_id IS NOT NULL AND target_landing_id IS NULL AND target_host = '' AND target_port IS NULL)
				OR
				(target_type = 'landing' AND target_proxy_id IS NULL AND target_client_id IS NULL AND target_landing_id IS NOT NULL AND target_host = '' AND target_port IS NULL)
				OR
				(target_type = 'manual' AND target_proxy_id IS NULL AND target_client_id IS NULL AND target_landing_id IS NULL AND target_host != '' AND target_port IS NOT NULL)
			)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relays_server_id ON relays(server_id)`,
		`CREATE INDEX IF NOT EXISTS idx_relays_target_proxy_id ON relays(target_proxy_id)`,
		`CREATE TABLE IF NOT EXISTS user_relay_order (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			relay_id INTEGER NOT NULL REFERENCES relays(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			PRIMARY KEY (user_id, relay_id),
			UNIQUE (user_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			credential_json TEXT NOT NULL,
			client_udp443 INTEGER NOT NULL DEFAULT 0 CHECK (client_udp443 IN (0, 1)),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			expires_at INTEGER,
			traffic_limit_bytes INTEGER CHECK (traffic_limit_bytes >= 0),
			traffic_reset_mode TEXT NOT NULL DEFAULT 'never'
				CHECK (traffic_reset_mode IN ('never', 'daily', 'weekly', 'monthly')),
			traffic_reset_weekday INTEGER NOT NULL DEFAULT 1
				CHECK (traffic_reset_weekday BETWEEN 1 AND 7),
			traffic_reset_day INTEGER NOT NULL DEFAULT 1
				CHECK (traffic_reset_day BETWEEN 1 AND 31),
			traffic_reset_time TEXT NOT NULL DEFAULT '00:00',
			effective_enabled_snapshot INTEGER NOT NULL DEFAULT 1 CHECK (effective_enabled_snapshot IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clients_proxy_id ON clients(proxy_id)`,
		`CREATE TABLE IF NOT EXISTS client_metrics (
			client_id INTEGER PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
			xray_uplink_bytes INTEGER NOT NULL CHECK (xray_uplink_bytes >= 0),
			xray_downlink_bytes INTEGER NOT NULL CHECK (xray_downlink_bytes >= 0),
			cycle_uplink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (cycle_uplink_bytes >= 0),
			cycle_downlink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (cycle_downlink_bytes >= 0),
			cycle_started_at INTEGER NOT NULL,
			last_activity_at INTEGER,
			updated_at INTEGER NOT NULL
		)`,
	}
}
