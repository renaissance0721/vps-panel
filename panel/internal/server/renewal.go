package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

var renewalLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type RenewalSettingsUpdate struct {
	ExpiresAtSet        bool
	ExpiresAt           *time.Time
	RenewalPeriodSet    bool
	RenewalPeriodMonths *int
	AutoRenewSet        bool
	AutoRenew           bool
}

func validRenewalPeriod(months int) bool {
	switch months {
	case 1, 3, 6, 12, 24, 36:
		return true
	default:
		return false
	}
}

func nextRenewalExpiration(current time.Time, periodMonths, anchorDay int) (time.Time, error) {
	if !validRenewalPeriod(periodMonths) || anchorDay < 1 || anchorDay > 31 {
		return time.Time{}, ErrInvalidRenewalPeriod
	}
	local := current.In(renewalLocation)
	targetMonth := time.Date(local.Year(), local.Month()+time.Month(periodMonths), 1, 0, 0, 0, 0, renewalLocation)
	lastDay := time.Date(targetMonth.Year(), targetMonth.Month()+1, 0, 0, 0, 0, 0, renewalLocation).Day()
	day := anchorDay
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetMonth.Year(), targetMonth.Month(), day, 23, 59, 59, 0, renewalLocation).UTC(), nil
}

func (s *Service) UpdateRenewalSettings(ctx context.Context, id int64, update RenewalSettingsUpdate) (Server, error) {
	if update.RenewalPeriodSet && update.RenewalPeriodMonths != nil && !validRenewalPeriod(*update.RenewalPeriodMonths) {
		return Server{}, ErrInvalidRenewalPeriod
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("begin server renewal update: %w", err)
	}
	defer tx.Rollback()

	var currentExpiration, currentPeriod, currentAnchor sql.NullInt64
	var currentAutoRenew bool
	if err := tx.QueryRowContext(ctx,
		`SELECT expires_at, renewal_period_months, auto_renew, renewal_anchor_day
		 FROM servers WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&currentExpiration, &currentPeriod, &currentAutoRenew, &currentAnchor); err != nil {
		if err == sql.ErrNoRows {
			return Server{}, ErrNotFound
		}
		return Server{}, fmt.Errorf("read server renewal settings: %w", err)
	}

	expiresAt := currentExpiration
	period := currentPeriod
	autoRenew := currentAutoRenew
	anchor := currentAnchor
	if update.ExpiresAtSet {
		expiresAt = sql.NullInt64{}
		if update.ExpiresAt != nil {
			expiresAt = sql.NullInt64{Int64: update.ExpiresAt.UTC().Truncate(time.Second).Unix(), Valid: true}
		}
	}
	if update.RenewalPeriodSet {
		period = sql.NullInt64{}
		if update.RenewalPeriodMonths != nil {
			period = sql.NullInt64{Int64: int64(*update.RenewalPeriodMonths), Valid: true}
		}
	}
	if update.AutoRenewSet {
		autoRenew = update.AutoRenew
	}
	if update.AutoRenewSet && update.AutoRenew && (!expiresAt.Valid || !period.Valid) {
		return Server{}, ErrAutoRenewRequirements
	}

	if !expiresAt.Valid {
		period = sql.NullInt64{}
		autoRenew = false
		anchor = sql.NullInt64{}
	} else if !period.Valid {
		autoRenew = false
		anchor = sql.NullInt64{}
	} else if update.ExpiresAtSet || (update.RenewalPeriodSet && !anchor.Valid) {
		anchor = sql.NullInt64{
			Int64: int64(time.Unix(expiresAt.Int64, 0).In(renewalLocation).Day()),
			Valid: true,
		}
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE servers
		 SET expires_at = ?, renewal_period_months = ?, auto_renew = ?, renewal_anchor_day = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		nullInt64Value(expiresAt), nullInt64Value(period), autoRenew, nullInt64Value(anchor),
		s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server renewal settings: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server renewal count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("commit server renewal update: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) ApplyAutomaticRenewals(ctx context.Context) error {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin automatic server renewal: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, expires_at, renewal_period_months, renewal_anchor_day
		 FROM servers
		 WHERE archived_at IS NULL AND auto_renew = 1
		   AND expires_at IS NOT NULL AND renewal_period_months IS NOT NULL AND expires_at <= ?`,
		now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("list expired servers for automatic renewal: %w", err)
	}
	type renewalCandidate struct {
		id        int64
		expiresAt int64
		period    int
		anchorDay sql.NullInt64
	}
	candidates := make([]renewalCandidate, 0)
	for rows.Next() {
		var candidate renewalCandidate
		if err := rows.Scan(&candidate.id, &candidate.expiresAt, &candidate.period, &candidate.anchorDay); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired server for automatic renewal: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close automatic renewal query: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired servers for automatic renewal: %w", err)
	}

	for _, candidate := range candidates {
		expiresAt := time.Unix(candidate.expiresAt, 0).UTC()
		anchorDay := int(candidate.anchorDay.Int64)
		if !candidate.anchorDay.Valid {
			anchorDay = expiresAt.In(renewalLocation).Day()
		}
		for !expiresAt.After(now) {
			expiresAt, err = nextRenewalExpiration(expiresAt, candidate.period, anchorDay)
			if err != nil {
				return fmt.Errorf("renew server %d: %w", candidate.id, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE servers SET expires_at = ?, renewal_anchor_day = ?, updated_at = ? WHERE id = ?`,
			expiresAt.Unix(), anchorDay, now.Unix(), candidate.id,
		); err != nil {
			return fmt.Errorf("store automatic renewal for server %d: %w", candidate.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit automatic server renewal: %w", err)
	}
	return nil
}

func nullInt64Value(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
