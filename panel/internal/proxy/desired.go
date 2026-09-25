package proxy

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DesiredProxy struct {
	ID          int64               `json:"id"`
	Listen      string              `json:"listen"`
	Port        int                 `json:"port"`
	Protocol    string              `json:"protocol"`
	Transport   string              `json:"transport,omitempty"`
	Security    string              `json:"security,omitempty"`
	ServerFlow  string              `json:"server_flow,omitempty"`
	ServerName  string              `json:"server_name,omitempty"`
	TLS         *DesiredTLS         `json:"tls,omitempty"`
	Reality     *DesiredReality     `json:"reality,omitempty"`
	Shadowsocks *DesiredShadowsocks `json:"shadowsocks,omitempty"`
	Clients     []DesiredClient     `json:"clients"`
}

type DesiredShadowsocks struct {
	Method   string `json:"method"`
	Network  string `json:"network"`
	Password string `json:"password"`
}

type DesiredTLS struct {
	Mode        string `json:"mode"`
	Certificate string `json:"certificate,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
}

type DesiredReality struct {
	Target     string `json:"target"`
	PrivateKey string `json:"private_key"`
	ShortID    string `json:"short_id"`
}

type DesiredClient struct {
	ID       int64  `json:"id"`
	StatsID  string `json:"stats_id"`
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
}

func ListDesired(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID int64) ([]DesiredProxy, error) {
	rows, err := query.QueryContext(ctx,
		`SELECT id, listen_port, protocol, config_json FROM proxies
		 WHERE server_id = ? AND enabled = 1 ORDER BY id`, serverID,
	)
	if err != nil {
		return nil, fmt.Errorf("list desired proxies: %w", err)
	}
	type desiredRecord struct {
		value  DesiredProxy
		config storedConfig
	}
	records := make([]desiredRecord, 0)
	for rows.Next() {
		var value DesiredProxy
		var configJSON string
		if err := rows.Scan(&value.ID, &value.Port, &value.Protocol, &configJSON); err != nil {
			return nil, fmt.Errorf("scan desired proxy: %w", err)
		}
		config, err := decodeConfig(value.Protocol, configJSON)
		if err != nil {
			return nil, err
		}
		value.Listen = "0.0.0.0"
		if value.Protocol == ProtocolVLESS {
			value.Transport = config.Transport
			value.Security = config.Security
			value.ServerFlow = config.ServerFlow
			value.ServerName = config.ServerName
		} else {
			value.Shadowsocks = &DesiredShadowsocks{
				Method: config.Shadowsocks.Method, Network: config.Shadowsocks.Network, Password: config.Shadowsocks.Password,
			}
		}
		records = append(records, desiredRecord{value: value, config: config})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate desired proxies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close desired proxies: %w", err)
	}
	values := make([]DesiredProxy, 0, len(records))
	for _, record := range records {
		value, config := record.value, record.config
		if config.TLS != nil {
			value.TLS = &DesiredTLS{Mode: config.TLS.Mode, Certificate: config.TLS.Certificate, PrivateKey: config.TLS.PrivateKey}
		}
		if config.Reality != nil {
			value.Reality = &DesiredReality{Target: config.Reality.Target, PrivateKey: config.Reality.PrivateKey, ShortID: config.Reality.ShortID}
		}
		clientRows, err := query.QueryContext(ctx,
			`SELECT id, credential_json FROM clients WHERE proxy_id = ? AND effective_enabled_snapshot = 1 ORDER BY id`, value.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("list desired clients: %w", err)
		}
		value.Clients = make([]DesiredClient, 0)
		for clientRows.Next() {
			var client DesiredClient
			var credentialJSON string
			if err := clientRows.Scan(&client.ID, &credentialJSON); err != nil {
				clientRows.Close()
				return nil, fmt.Errorf("scan desired client: %w", err)
			}
			credential, err := decodeCredential(value.Protocol, credentialJSON)
			if err != nil {
				clientRows.Close()
				return nil, err
			}
			client.UUID = credential.UUID
			client.Password = credential.Password
			client.StatsID = clientStatsIdentifier(client.ID)
			if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(client.Password, config.Shadowsocks.Method) {
				clientRows.Close()
				return nil, ErrInvalidShadowsocksCredential
			}
			value.Clients = append(value.Clients, client)
		}
		if err := clientRows.Close(); err != nil {
			return nil, fmt.Errorf("close desired clients: %w", err)
		}
		if value.Protocol == ProtocolShadowsocks && len(value.Clients) == 0 {
			continue
		}
		values = append(values, value)
	}
	return values, nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND decommission_status = ''`, now.Unix(), serverID,
	)
	if err != nil {
		return 0, fmt.Errorf("bump desired state version: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read desired state update count: %w", err)
	}
	if count != 1 {
		var status string
		if err := tx.QueryRowContext(ctx,
			`SELECT decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
		).Scan(&status); err == nil && status != "" {
			return 0, ErrServerDecommissioning
		}
		return 0, ErrServerNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now.Unix(), serverID,
	); err != nil {
		return 0, fmt.Errorf("mark Agent config sync pending: %w", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
		return 0, fmt.Errorf("read desired state version: %w", err)
	}
	return version, nil
}
