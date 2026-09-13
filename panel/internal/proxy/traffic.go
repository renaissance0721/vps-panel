package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const clientStatsIdentifierPrefix = "vp-client-"

const (
	ClientStatusDisabled  = "disabled"
	ClientStatusNormal    = "normal"
	ClientStatusWarning   = "warning"
	ClientStatusExhausted = "exhausted"
	ClientStatusExpired   = "expired"
)

var clientTrafficLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

var ErrInvalidClientTraffic = errors.New("invalid client traffic report")

type ClientMetrics struct {
	XrayUplinkBytes    int64
	XrayDownlinkBytes  int64
	CycleUplinkBytes   int64
	CycleDownlinkBytes int64
	CycleStartedAt     time.Time
	LastActivityAt     *time.Time
	UpdatedAt          time.Time
}

type ClientLifecycle struct {
	Expired          bool
	QuotaExhausted   bool
	EffectiveEnabled bool
	Status           string
}

type ClientTrafficReport struct {
	ClientID      int64
	UplinkBytes   int64
	DownlinkBytes int64
}

func ClientUsedBytes(metrics *ClientMetrics) int64 {
	if metrics == nil {
		return 0
	}
	if metrics.CycleUplinkBytes > math.MaxInt64-metrics.CycleDownlinkBytes {
		return math.MaxInt64
	}
	return metrics.CycleUplinkBytes + metrics.CycleDownlinkBytes
}

func (value Client) LifecycleAt(now time.Time) ClientLifecycle {
	used := ClientUsedBytes(value.Metrics)
	expired := value.ExpiresAt != nil && !now.UTC().Before(value.ExpiresAt.UTC())
	quotaExhausted := value.TrafficLimitBytes != nil && *value.TrafficLimitBytes > 0 && used >= *value.TrafficLimitBytes
	lifecycle := ClientLifecycle{
		Expired: expired, QuotaExhausted: quotaExhausted,
		EffectiveEnabled: value.Enabled && !expired && !quotaExhausted,
		Status:           ClientStatusNormal,
	}
	switch {
	case !value.Enabled:
		lifecycle.Status = ClientStatusDisabled
	case expired:
		lifecycle.Status = ClientStatusExpired
	case quotaExhausted:
		lifecycle.Status = ClientStatusExhausted
	case value.TrafficLimitBytes != nil && *value.TrafficLimitBytes > 0 &&
		used >= *value.TrafficLimitBytes-*value.TrafficLimitBytes/10:
		lifecycle.Status = ClientStatusWarning
	}
	return lifecycle
}

func (s *Service) RecordClientTraffic(ctx context.Context, serverID int64, reports []ClientTrafficReport) error {
	_, err := s.RecordClientTrafficWithMutation(ctx, serverID, reports)
	return err
}

func (s *Service) RecordClientTrafficWithMutation(ctx context.Context, serverID int64, reports []ClientTrafficReport) (Mutation, error) {
	if serverID <= 0 {
		return Mutation{}, ErrInvalidClientTraffic
	}
	seen := make(map[int64]struct{}, len(reports))
	for _, report := range reports {
		if report.ClientID <= 0 || report.UplinkBytes < 0 || report.DownlinkBytes < 0 {
			return Mutation{}, ErrInvalidClientTraffic
		}
		if _, exists := seen[report.ClientID]; exists {
			return Mutation{}, ErrInvalidClientTraffic
		}
		seen[report.ClientID] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin client traffic report: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Second)
	lifecycleChanged := false
	for _, report := range reports {
		changed, err := recordClientTraffic(ctx, tx, serverID, report, now)
		if err != nil {
			return Mutation{}, err
		}
		lifecycleChanged = lifecycleChanged || changed
	}
	var mutation Mutation
	if lifecycleChanged {
		version, err := bumpVersion(ctx, tx, serverID, now)
		if err != nil {
			return Mutation{}, err
		}
		mutation = Mutation{ServerID: serverID, Version: version}
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit client traffic report: %w", err)
	}
	return mutation, nil
}

func recordClientTraffic(ctx context.Context, tx *sql.Tx, serverID int64, report ClientTrafficReport, now time.Time) (bool, error) {
	var previousUplink, previousDownlink, cycleUplink, cycleDownlink sql.NullInt64
	var cycleStartedAt, lastActivityAt sql.NullInt64
	var expiresAt, trafficLimit sql.NullInt64
	var resetMode, resetTime string
	var resetWeekday, resetDay, enabled, effectiveEnabled int
	err := tx.QueryRowContext(ctx,
		`SELECT metrics.xray_uplink_bytes, metrics.xray_downlink_bytes,
		 metrics.cycle_uplink_bytes, metrics.cycle_downlink_bytes,
		 metrics.cycle_started_at, metrics.last_activity_at,
		 clients.enabled, clients.expires_at, clients.traffic_limit_bytes, clients.effective_enabled_snapshot,
		 clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE clients.id = ? AND proxies.server_id = ? AND servers.archived_at IS NULL`,
		report.ClientID, serverID,
	).Scan(
		&previousUplink, &previousDownlink, &cycleUplink, &cycleDownlink,
		&cycleStartedAt, &lastActivityAt, &enabled, &expiresAt, &trafficLimit, &effectiveEnabled,
		&resetMode, &resetWeekday, &resetDay, &resetTime,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrInvalidClientTraffic
	}
	if err != nil {
		return false, fmt.Errorf("read client traffic baseline: %w", err)
	}
	client := Client{Enabled: enabled != 0, ExpiresAt: nullableTimeValue(expiresAt)}
	if trafficLimit.Valid && trafficLimit.Int64 > 0 {
		limit := trafficLimit.Int64
		client.TrafficLimitBytes = &limit
	}
	updateLifecycle := func(uplink, downlink int64) (bool, error) {
		client.Metrics = &ClientMetrics{CycleUplinkBytes: uplink, CycleDownlinkBytes: downlink}
		next := client.LifecycleAt(now).EffectiveEnabled
		if next == (effectiveEnabled != 0) {
			return false, nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE clients SET effective_enabled_snapshot = ? WHERE id = ?`, next, report.ClientID,
		); err != nil {
			return false, fmt.Errorf("update client effective state: %w", err)
		}
		return true, nil
	}

	if !previousUplink.Valid {
		cycleStart, err := currentClientCycleStart(now, resetMode, resetWeekday, resetDay, resetTime)
		if err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO client_metrics
			 (client_id, xray_uplink_bytes, xray_downlink_bytes,
			  cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
			 VALUES (?, ?, ?, 0, 0, ?, ?)`,
			report.ClientID, report.UplinkBytes, report.DownlinkBytes, cycleStart.Unix(), now.Unix(),
		)
		if err != nil {
			return false, fmt.Errorf("create client traffic baseline: %w", err)
		}
		return updateLifecycle(0, 0)
	}

	cycleStart := time.Unix(cycleStartedAt.Int64, 0).UTC()
	currentCycleStart, err := currentClientCycleStart(now, resetMode, resetWeekday, resetDay, resetTime)
	if err != nil {
		return false, err
	}
	if resetMode != TrafficResetNever && cycleStart.Before(currentCycleStart) {
		_, err = tx.ExecContext(ctx,
			`UPDATE client_metrics
			 SET xray_uplink_bytes = ?, xray_downlink_bytes = ?,
			     cycle_uplink_bytes = 0, cycle_downlink_bytes = 0,
			     cycle_started_at = ?, updated_at = ?
			 WHERE client_id = ?`,
			report.UplinkBytes, report.DownlinkBytes, currentCycleStart.Unix(), now.Unix(), report.ClientID,
		)
		if err != nil {
			return false, fmt.Errorf("reset client traffic cycle: %w", err)
		}
		return updateLifecycle(0, 0)
	}

	deltaUplink := counterDelta(previousUplink.Int64, report.UplinkBytes)
	deltaDownlink := counterDelta(previousDownlink.Int64, report.DownlinkBytes)
	if cycleUplink.Int64 > math.MaxInt64-deltaUplink || cycleDownlink.Int64 > math.MaxInt64-deltaDownlink {
		return false, ErrInvalidClientTraffic
	}
	newCycleUplink := cycleUplink.Int64 + deltaUplink
	newCycleDownlink := cycleDownlink.Int64 + deltaDownlink
	if newCycleUplink > math.MaxInt64-newCycleDownlink {
		return false, ErrInvalidClientTraffic
	}
	if deltaUplink > 0 || deltaDownlink > 0 {
		lastActivityAt = sql.NullInt64{Int64: now.Unix(), Valid: true}
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE client_metrics
		 SET xray_uplink_bytes = ?, xray_downlink_bytes = ?,
		     cycle_uplink_bytes = ?, cycle_downlink_bytes = ?,
		     last_activity_at = ?, updated_at = ?
		 WHERE client_id = ?`,
		report.UplinkBytes, report.DownlinkBytes, newCycleUplink, newCycleDownlink,
		nullableUnix(lastActivityAt), now.Unix(), report.ClientID,
	)
	if err != nil {
		return false, fmt.Errorf("update client traffic: %w", err)
	}
	return updateLifecycle(newCycleUplink, newCycleDownlink)
}

func normalizeClientTrafficConfig(value ClientTrafficConfig) (ClientTrafficConfig, error) {
	if value.ResetMode == "" {
		value.ResetMode = TrafficResetNever
	}
	if value.Weekday == 0 {
		value.Weekday = 1
	}
	if value.Day == 0 {
		value.Day = 1
	}
	if value.ResetTime == "" {
		value.ResetTime = "00:00"
	}
	if value.LimitBytes != nil && *value.LimitBytes <= 0 {
		value.LimitBytes = nil
	}
	if value.Weekday < 1 || value.Weekday > 7 || value.Day < 1 || value.Day > 31 ||
		!validClientResetTime(value.ResetTime) {
		return ClientTrafficConfig{}, ErrInvalidClientTrafficConfig
	}
	switch value.ResetMode {
	case TrafficResetNever, TrafficResetDaily, TrafficResetWeekly, TrafficResetMonthly:
	default:
		return ClientTrafficConfig{}, ErrInvalidClientTrafficConfig
	}
	return value, nil
}

func nullableTrafficLimit(value *int64) any {
	if value == nil || *value <= 0 {
		return nil
	}
	return *value
}

func validClientResetTime(value string) bool {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	return err == nil && parsed.Format("15:04") == value
}

func currentClientCycleStart(now time.Time, mode string, weekday, day int, resetTime string) (time.Time, error) {
	config, err := normalizeClientTrafficConfig(ClientTrafficConfig{
		ResetMode: mode, Weekday: weekday, Day: day, ResetTime: resetTime,
	})
	if err != nil {
		return time.Time{}, err
	}
	localNow := now.In(clientTrafficLocation)
	parsed, _ := time.Parse("15:04", config.ResetTime)
	hour, minute := parsed.Hour(), parsed.Minute()
	var boundary time.Time
	switch config.ResetMode {
	case TrafficResetNever:
		return now.UTC().Truncate(time.Second), nil
	case TrafficResetDaily:
		boundary = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, clientTrafficLocation)
		if localNow.Before(boundary) {
			boundary = boundary.AddDate(0, 0, -1)
		}
	case TrafficResetWeekly:
		isoWeekday := int(localNow.Weekday())
		if isoWeekday == 0 {
			isoWeekday = 7
		}
		boundary = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, clientTrafficLocation).
			AddDate(0, 0, -(isoWeekday-config.Weekday+7)%7)
		if localNow.Before(boundary) {
			boundary = boundary.AddDate(0, 0, -7)
		}
	case TrafficResetMonthly:
		boundary = clientMonthlyBoundary(localNow.Year(), localNow.Month(), config.Day, hour, minute)
		if localNow.Before(boundary) {
			previous := time.Date(localNow.Year(), localNow.Month()-1, 1, 0, 0, 0, 0, clientTrafficLocation)
			boundary = clientMonthlyBoundary(previous.Year(), previous.Month(), config.Day, hour, minute)
		}
	}
	return boundary.UTC(), nil
}

func nextClientResetAt(now time.Time, mode string, weekday, day int, resetTime string) (*time.Time, error) {
	config, err := normalizeClientTrafficConfig(ClientTrafficConfig{
		ResetMode: mode, Weekday: weekday, Day: day, ResetTime: resetTime,
	})
	if err != nil {
		return nil, err
	}
	if config.ResetMode == TrafficResetNever {
		return nil, nil
	}
	current, err := currentClientCycleStart(now, config.ResetMode, config.Weekday, config.Day, config.ResetTime)
	if err != nil {
		return nil, err
	}
	localCurrent := current.In(clientTrafficLocation)
	var next time.Time
	switch config.ResetMode {
	case TrafficResetDaily:
		next = localCurrent.AddDate(0, 0, 1)
	case TrafficResetWeekly:
		next = localCurrent.AddDate(0, 0, 7)
	case TrafficResetMonthly:
		month := time.Date(localCurrent.Year(), localCurrent.Month()+1, 1, 0, 0, 0, 0, clientTrafficLocation)
		next = clientMonthlyBoundary(month.Year(), month.Month(), config.Day, localCurrent.Hour(), localCurrent.Minute())
	}
	result := next.UTC()
	return &result, nil
}

func clientMonthlyBoundary(year int, month time.Month, day, hour, minute int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, clientTrafficLocation).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, hour, minute, 0, 0, clientTrafficLocation)
}

func (value Client) NextResetAt(now time.Time) *time.Time {
	next, _ := nextClientResetAt(
		now, value.TrafficResetMode, value.TrafficResetWeekday,
		value.TrafficResetDay, value.TrafficResetTime,
	)
	return next
}

func (s *Service) ResetClientTraffic(ctx context.Context, clientID int64) (Client, error) {
	value, _, err := s.ResetClientTrafficWithMutation(ctx, clientID)
	return value, err
}

func (s *Service) ResetClientTrafficWithMutation(ctx context.Context, clientID int64) (Client, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("begin client traffic reset: %w", err)
	}
	defer tx.Rollback()
	value, serverID, err := getClientForMutation(ctx, tx, clientID)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE client_metrics
		 SET cycle_uplink_bytes = 0, cycle_downlink_bytes = 0,
		     cycle_started_at = ?, updated_at = ?
		 WHERE client_id = ?`, now.Unix(), now.Unix(), clientID,
	); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("reset client traffic: %w", err)
	}
	value.Metrics = &ClientMetrics{}
	nextEffective := value.LifecycleAt(now).EffectiveEnabled
	lifecycleChanged := nextEffective != value.effectiveEnabled
	if lifecycleChanged {
		if _, err := tx.ExecContext(ctx,
			`UPDATE clients SET effective_enabled_snapshot = ? WHERE id = ?`, nextEffective, clientID,
		); err != nil {
			return Client{}, Mutation{}, fmt.Errorf("update reset client effective state: %w", err)
		}
	}
	var mutation Mutation
	if lifecycleChanged {
		version, err := bumpVersion(ctx, tx, serverID, now)
		if err != nil {
			return Client{}, Mutation{}, err
		}
		mutation = Mutation{ServerID: serverID, Version: version}
	}
	if err := tx.Commit(); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("commit client traffic reset: %w", err)
	}
	updated, err := s.GetClient(ctx, clientID)
	return updated, mutation, err
}

func ReconcileClientLifecycle(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) (Mutation, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT clients.id, clients.enabled, clients.expires_at, clients.traffic_limit_bytes,
		 clients.effective_enabled_snapshot, clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time,
		 metrics.cycle_started_at,
		 COALESCE(metrics.cycle_uplink_bytes, 0), COALESCE(metrics.cycle_downlink_bytes, 0)
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE proxies.server_id = ? AND servers.archived_at IS NULL`, serverID,
	)
	if err != nil {
		return Mutation{}, fmt.Errorf("list client lifecycle state: %w", err)
	}
	type lifecycleUpdate struct {
		id               int64
		effective        bool
		effectiveChanged bool
		cycleStart       *time.Time
	}
	updates := make([]lifecycleUpdate, 0)
	for rows.Next() {
		var id, usedUplink, usedDownlink int64
		var enabled, effectiveEnabled int
		var resetMode, resetTime string
		var resetWeekday, resetDay int
		var expiresAt, trafficLimit, cycleStartedAt sql.NullInt64
		if err := rows.Scan(
			&id, &enabled, &expiresAt, &trafficLimit, &effectiveEnabled,
			&resetMode, &resetWeekday, &resetDay, &resetTime, &cycleStartedAt,
			&usedUplink, &usedDownlink,
		); err != nil {
			rows.Close()
			return Mutation{}, fmt.Errorf("scan client lifecycle state: %w", err)
		}
		value := Client{
			Enabled: enabled != 0, ExpiresAt: nullableTimeValue(expiresAt),
			Metrics: &ClientMetrics{CycleUplinkBytes: usedUplink, CycleDownlinkBytes: usedDownlink},
		}
		if trafficLimit.Valid && trafficLimit.Int64 > 0 {
			limit := trafficLimit.Int64
			value.TrafficLimitBytes = &limit
		}
		var resetAt *time.Time
		if cycleStartedAt.Valid && resetMode != TrafficResetNever {
			currentCycleStart, err := currentClientCycleStart(now, resetMode, resetWeekday, resetDay, resetTime)
			if err != nil {
				rows.Close()
				return Mutation{}, err
			}
			if time.Unix(cycleStartedAt.Int64, 0).UTC().Before(currentCycleStart) {
				usedUplink, usedDownlink = 0, 0
				resetAt = &currentCycleStart
				value.Metrics = &ClientMetrics{}
			}
		}
		effective := value.LifecycleAt(now).EffectiveEnabled
		changed := effective != (effectiveEnabled != 0)
		if changed || resetAt != nil {
			updates = append(updates, lifecycleUpdate{
				id: id, effective: effective, effectiveChanged: changed, cycleStart: resetAt,
			})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Mutation{}, fmt.Errorf("iterate client lifecycle state: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Mutation{}, fmt.Errorf("close client lifecycle state: %w", err)
	}
	effectiveChanged := false
	for _, update := range updates {
		if update.cycleStart != nil {
			if _, err := tx.ExecContext(ctx,
				`UPDATE client_metrics SET cycle_uplink_bytes = 0, cycle_downlink_bytes = 0,
				 cycle_started_at = ?, updated_at = ? WHERE client_id = ?`,
				update.cycleStart.Unix(), now.Unix(), update.id,
			); err != nil {
				return Mutation{}, fmt.Errorf("reconcile client traffic cycle: %w", err)
			}
		}
		if update.effectiveChanged {
			effectiveChanged = true
			if _, err := tx.ExecContext(ctx,
				`UPDATE clients SET effective_enabled_snapshot = ? WHERE id = ?`, update.effective, update.id,
			); err != nil {
				return Mutation{}, fmt.Errorf("reconcile client effective state: %w", err)
			}
		}
	}
	if !effectiveChanged {
		return Mutation{}, nil
	}
	version, err := bumpVersion(ctx, tx, serverID, now.UTC().Truncate(time.Second))
	if err != nil {
		return Mutation{}, err
	}
	return Mutation{ServerID: serverID, Version: version}, nil
}

func counterDelta(previous, current int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func (s *Service) getClientMetrics(ctx context.Context, clientID int64) (*ClientMetrics, error) {
	var uplink, downlink, cycleUplink, cycleDownlink, cycleStartedAt, updatedAt int64
	var lastActivityAt sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes,
		 cycle_downlink_bytes, cycle_started_at, last_activity_at, updated_at
		 FROM client_metrics WHERE client_id = ?`, clientID,
	).Scan(&uplink, &downlink, &cycleUplink, &cycleDownlink, &cycleStartedAt, &lastActivityAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read client metrics: %w", err)
	}
	return clientMetricsFromDatabase(
		uplink, downlink, cycleUplink, cycleDownlink, cycleStartedAt, lastActivityAt, updatedAt,
	), nil
}

func clientMetricsFromDatabase(
	xrayUplink, xrayDownlink, cycleUplink, cycleDownlink, cycleStartedAt int64,
	lastActivityAt sql.NullInt64,
	updatedAt int64,
) *ClientMetrics {
	metrics := &ClientMetrics{
		XrayUplinkBytes: xrayUplink, XrayDownlinkBytes: xrayDownlink,
		CycleUplinkBytes: cycleUplink, CycleDownlinkBytes: cycleDownlink,
		CycleStartedAt: time.Unix(cycleStartedAt, 0).UTC(), UpdatedAt: time.Unix(updatedAt, 0).UTC(),
	}
	if lastActivityAt.Valid {
		value := time.Unix(lastActivityAt.Int64, 0).UTC()
		metrics.LastActivityAt = &value
	}
	return metrics
}

func nullableUnix(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func clientStatsIdentifier(clientID int64) string {
	return clientStatsIdentifierPrefix + strconv.FormatInt(clientID, 10)
}
