package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/operation"
)

const MaxChinaWhitelistEntries = 128

var ErrInvalidChinaWhitelist = errors.New("invalid China inbound whitelist")

type ChinaWhitelistEntry struct {
	Address string `json:"address"`
	Remark  string `json:"remark"`
}

func normalizeChinaWhitelist(entries []ChinaWhitelistEntry) ([]ChinaWhitelistEntry, error) {
	if len(entries) > MaxChinaWhitelistEntries {
		return nil, ErrInvalidChinaWhitelist
	}
	result := make([]ChinaWhitelistEntry, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		raw := strings.TrimSpace(entry.Address)
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, addrErr := netip.ParseAddr(raw)
			if addrErr != nil || addr.Zone() != "" || addr.Is4In6() {
				return nil, ErrInvalidChinaWhitelist
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Bits() == 0 {
			return nil, ErrInvalidChinaWhitelist
		}
		entry.Address = prefix.Masked().String()
		entry.Remark = strings.TrimSpace(entry.Remark)
		if seen[entry.Address] || utf8.RuneCountInString(entry.Remark) > 100 {
			return nil, ErrInvalidChinaWhitelist
		}
		seen[entry.Address] = true
		result = append(result, entry)
	}
	return result, nil
}

type chinaWhitelistQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listChinaWhitelist(ctx context.Context, db chinaWhitelistQueryer, id int64) ([]ChinaWhitelistEntry, error) {
	rows, err := db.QueryContext(ctx, `SELECT address, remark FROM server_china_inbound_whitelist WHERE server_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []ChinaWhitelistEntry{}
	for rows.Next() {
		var entry ChinaWhitelistEntry
		if err := rows.Scan(&entry.Address, &entry.Remark); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Service) ChinaWhitelist(ctx context.Context, id int64) ([]ChinaWhitelistEntry, error) {
	return listChinaWhitelist(ctx, s.db, id)
}

func (s *Service) UpdateChinaWhitelist(ctx context.Context, id int64, input []ChinaWhitelistEntry) (Server, int64, bool, error) {
	entries, err := normalizeChinaWhitelist(input)
	if err != nil {
		return Server{}, 0, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, 0, false, err
	}
	defer tx.Rollback()
	var version int64
	var lifecycle string
	if err := tx.QueryRowContext(ctx, `SELECT desired_state_version, decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, id).Scan(&version, &lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Server{}, 0, false, ErrNotFound
		}
		return Server{}, 0, false, err
	}
	if lifecycle != "" {
		return Server{}, 0, false, ErrDecommissioning
	}
	previous, err := listChinaWhitelist(ctx, tx, id)
	if err != nil {
		return Server{}, 0, false, err
	}
	if reflect.DeepEqual(previous, entries) {
		_ = tx.Rollback()
		value, err := s.Get(ctx, id)
		return value, version, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM server_china_inbound_whitelist WHERE server_id = ?`, id); err != nil {
		return Server{}, 0, false, err
	}
	for i, entry := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO server_china_inbound_whitelist (server_id, address, remark, position) VALUES (?, ?, ?, ?)`, id, entry.Address, entry.Remark, i); err != nil {
			return Server{}, 0, false, err
		}
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err = tx.ExecContext(ctx, `UPDATE servers SET desired_state_version = desired_state_version + 1, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return Server{}, 0, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`, now, id); err != nil {
		return Server{}, 0, false, err
	}
	version++
	if err = operation.RecordTx(ctx, tx, id, "server", id, "update", version, time.Unix(now, 0)); err != nil {
		return Server{}, 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return Server{}, 0, false, fmt.Errorf("commit China whitelist: %w", err)
	}
	value, err := s.Get(ctx, id)
	return value, version, true, err
}
