CREATE TABLE subscription_published_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('direct', 'relay')),
    target_proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
    source_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
    relay_id INTEGER REFERENCES relays(id) ON DELETE SET NULL,
    traffic_multiplier_bp INTEGER NOT NULL DEFAULT 100 CHECK (traffic_multiplier_bp BETWEEN 10 AND 500),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((mode = 'direct' AND source_proxy_id IS NULL AND relay_id IS NULL)
        OR (mode = 'relay' AND source_proxy_id IS NOT NULL AND relay_id IS NOT NULL))
);
