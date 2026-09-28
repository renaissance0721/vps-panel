package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func (s *Service) ListSubscribers(ctx context.Context) ([]Subscriber, error) {
	rows, err := s.db.QueryContext(ctx, subscriberSelect+`
		WHERE users.role = 'subscriber' ORDER BY users.created_at DESC, users.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list subscribers: %w", err)
	}
	values := make([]Subscriber, 0)
	for rows.Next() {
		value, err := scanSubscriber(rows, s.now().UTC())
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan subscriber: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate subscribers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close subscribers: %w", err)
	}
	for index := range values {
		if err := s.loadSubscriberUsage(ctx, &values[index]); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (s *Service) GetSubscriber(ctx context.Context, userID int64) (Subscriber, error) {
	value, err := scanSubscriber(s.db.QueryRowContext(ctx, subscriberSelect+`
		WHERE users.id = ? AND users.role = 'subscriber'`, userID), s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return Subscriber{}, ErrSubscriberNotFound
	}
	if err != nil {
		return Subscriber{}, fmt.Errorf("get subscriber: %w", err)
	}
	if err := s.loadSubscriberUsage(ctx, &value); err != nil {
		return Subscriber{}, err
	}
	return value, nil
}

func (s *Service) UpdateSubscriber(ctx context.Context, userID int64, input UpdateSubscriberInput) (Subscriber, []proxystore.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Subscriber{}, nil, fmt.Errorf("begin subscriber update: %w", err)
	}
	defer tx.Rollback()
	var currentPlanID, currentExpiresAt sql.NullInt64
	var profileEnabled int
	err = tx.QueryRowContext(ctx, `SELECT profiles.plan_id, profiles.enabled, profiles.expires_at
		FROM subscriber_profiles AS profiles JOIN users ON users.id = profiles.user_id
		WHERE profiles.user_id = ? AND users.role = 'subscriber'`, userID).
		Scan(&currentPlanID, &profileEnabled, &currentExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Subscriber{}, nil, ErrSubscriberNotFound
	}
	if err != nil {
		return Subscriber{}, nil, fmt.Errorf("read subscriber for update: %w", err)
	}
	planID := nullableInt64Value(currentPlanID)
	if input.PlanIDSet {
		planID = input.PlanID
	}
	if planID != nil {
		var defaultValidityDays sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT default_validity_days FROM subscription_plans WHERE id = ?`, *planID,
		).Scan(&defaultValidityDays); errors.Is(err, sql.ErrNoRows) {
			return Subscriber{}, nil, ErrInvalidSubscriberPlan
		} else if err != nil {
			return Subscriber{}, nil, fmt.Errorf("validate subscriber plan: %w", err)
		}
		if input.PlanIDSet && !input.ExpiresAtSet && !currentExpiresAt.Valid && defaultValidityDays.Valid {
			expiresAt := now.AddDate(0, 0, int(defaultValidityDays.Int64))
			input.ExpiresAt = &expiresAt
			input.ExpiresAtSet = true
		}
	}
	enabled := profileEnabled != 0
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	expiresAt := nullableTimeValue(currentExpiresAt)
	if input.ExpiresAtSet {
		expiresAt = normalizeExpiration(input.ExpiresAt)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscriber_profiles
		SET plan_id = ?, enabled = ?, expires_at = ?, updated_at = ? WHERE user_id = ?`,
		nullableID(planID), enabled, nullableTime(expiresAt), now.Unix(), userID); err != nil {
		return Subscriber{}, nil, fmt.Errorf("update subscriber profile: %w", err)
	}
	affected := make(map[int64]struct{})
	if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
		return Subscriber{}, nil, err
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return Subscriber{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Subscriber{}, nil, fmt.Errorf("commit subscriber update: %w", err)
	}
	updated, err := s.GetSubscriber(ctx, userID)
	return updated, mutations, err
}

func (s *Service) ReconcileSubscriber(ctx context.Context, userID int64) ([]proxystore.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin subscriber reconciliation: %w", err)
	}
	defer tx.Rollback()
	affected := make(map[int64]struct{})
	if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
		return nil, err
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit subscriber reconciliation: %w", err)
	}
	return mutations, nil
}

func (s *Service) RegenerateSubscriptionToken(ctx context.Context, userID int64) (string, error) {
	tokenValue, _, err := token.New()
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE subscriber_profiles SET subscription_token = ?, updated_at = ?
		WHERE user_id = ? AND EXISTS(SELECT 1 FROM users WHERE id = ? AND role = 'subscriber')`,
		tokenValue, s.now().UTC().Truncate(time.Second).Unix(), userID, userID)
	if err != nil {
		return "", fmt.Errorf("regenerate subscription token: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("read regenerated subscription token count: %w", err)
	}
	if count != 1 {
		return "", ErrSubscriberNotFound
	}
	return tokenValue, nil
}

func (s *Service) reconcileSubscriberTx(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	now time.Time,
	affected map[int64]struct{},
) error {
	var username string
	var planID, expiresAt, trafficLimit sql.NullInt64
	var profileEnabled, planEnabled int
	var resetMode, resetTime string
	var resetDay int
	var archivedUplink, archivedDownlink, storedCycleStart int64
	err := tx.QueryRowContext(ctx, `SELECT users.username, profiles.plan_id, profiles.enabled, profiles.expires_at,
		COALESCE(plans.enabled, 0), plans.traffic_limit_bytes,
		COALESCE(plans.traffic_reset_mode, 'never'), COALESCE(plans.traffic_reset_day, 1),
		COALESCE(plans.traffic_reset_time, '00:00'), usage.archived_uplink_bytes,
		usage.archived_downlink_bytes, usage.cycle_started_at
		FROM subscriber_profiles AS profiles
		JOIN users ON users.id = profiles.user_id
		LEFT JOIN subscription_plans AS plans ON plans.id = profiles.plan_id
		JOIN subscriber_usage AS usage ON usage.user_id = profiles.user_id
		WHERE profiles.user_id = ? AND users.role = 'subscriber'`, userID).
		Scan(&username, &planID, &profileEnabled, &expiresAt, &planEnabled, &trafficLimit,
			&resetMode, &resetDay, &resetTime, &archivedUplink, &archivedDownlink, &storedCycleStart)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSubscriberNotFound
	}
	if err != nil {
		return fmt.Errorf("read subscriber reconciliation state: %w", err)
	}
	if resetMode == ResetModeMonthly {
		cycleStart, err := currentSubscriberCycleStart(now, resetMode, resetDay, resetTime)
		if err != nil {
			return err
		}
		if storedCycleStart < cycleStart.Unix() {
			if _, err := tx.ExecContext(ctx, `UPDATE subscriber_usage SET archived_uplink_bytes = 0,
				archived_downlink_bytes = 0, cycle_started_at = ?, updated_at = ? WHERE user_id = ?`,
				cycleStart.Unix(), now.Unix(), userID); err != nil {
				return fmt.Errorf("reset subscriber usage cycle: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE client_metrics SET cycle_uplink_bytes = 0,
				cycle_downlink_bytes = 0, cycle_started_at = ?, updated_at = ?
				WHERE client_id IN (SELECT client_id FROM subscriber_clients WHERE user_id = ?)`,
				cycleStart.Unix(), now.Unix(), userID); err != nil {
				return fmt.Errorf("reset subscriber client usage cycle: %w", err)
			}
			archivedUplink, archivedDownlink = 0, 0
		}
	}
	usedBytes, err := currentSubscriberUsage(ctx, tx, userID, archivedUplink, archivedDownlink)
	if err != nil {
		return err
	}
	quotaExhausted := trafficLimit.Valid && usedBytes >= trafficLimit.Int64
	active := profileEnabled != 0 && planID.Valid && planEnabled != 0 &&
		(!expiresAt.Valid || now.Unix() < expiresAt.Int64) && !quotaExhausted

	type requiredProxy struct {
		serverID int64
		name     string
	}
	required := make(map[int64]requiredProxy)
	if planID.Valid {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT proxies.id, proxies.server_id, proxies.name
			FROM subscription_plan_nodes AS mapping
			JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
			JOIN proxies ON proxies.id = nodes.target_proxy_id
			JOIN servers ON servers.id = proxies.server_id
			WHERE mapping.plan_id = ? AND nodes.enabled = 1 AND servers.archived_at IS NULL`, planID.Int64)
		if err != nil {
			return fmt.Errorf("list required subscriber proxies: %w", err)
		}
		for rows.Next() {
			var proxyID int64
			var value requiredProxy
			if err := rows.Scan(&proxyID, &value.serverID, &value.name); err != nil {
				rows.Close()
				return fmt.Errorf("scan required subscriber proxy: %w", err)
			}
			required[proxyID] = value
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate required subscriber proxies: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close required subscriber proxies: %w", err)
		}
	}

	type existingClient struct {
		clientID         int64
		serverID         int64
		effectiveEnabled bool
		cycleUplink      int64
		cycleDownlink    int64
	}
	existing := make(map[int64]existingClient)
	rows, err := tx.QueryContext(ctx, `SELECT mapping.proxy_id, mapping.client_id, proxies.server_id,
		clients.effective_enabled_snapshot, COALESCE(metrics.cycle_uplink_bytes, 0),
		COALESCE(metrics.cycle_downlink_bytes, 0)
		FROM subscriber_clients AS mapping
		JOIN clients ON clients.id = mapping.client_id
		JOIN proxies ON proxies.id = mapping.proxy_id
		LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		WHERE mapping.user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("list subscriber clients for reconciliation: %w", err)
	}
	for rows.Next() {
		var proxyID int64
		var value existingClient
		var effective int
		if err := rows.Scan(&proxyID, &value.clientID, &value.serverID, &effective,
			&value.cycleUplink, &value.cycleDownlink); err != nil {
			rows.Close()
			return fmt.Errorf("scan subscriber client for reconciliation: %w", err)
		}
		value.effectiveEnabled = effective != 0
		existing[proxyID] = value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate subscriber clients for reconciliation: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close subscriber clients for reconciliation: %w", err)
	}

	for proxyID, client := range existing {
		if _, needed := required[proxyID]; !needed {
			archivedUplink = saturatingAdd(archivedUplink, client.cycleUplink)
			archivedDownlink = saturatingAdd(archivedDownlink, client.cycleDownlink)
			if _, err := tx.ExecContext(ctx, `UPDATE subscriber_usage SET
				archived_uplink_bytes = ?, archived_downlink_bytes = ?, updated_at = ? WHERE user_id = ?`,
				archivedUplink, archivedDownlink, now.Unix(), userID); err != nil {
				return fmt.Errorf("archive removed subscriber client usage: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, client.clientID); err != nil {
				return fmt.Errorf("delete obsolete subscriber client: %w", err)
			}
			affected[client.serverID] = struct{}{}
			continue
		}
		if client.effectiveEnabled != active {
			if _, err := tx.ExecContext(ctx,
				`UPDATE clients SET effective_enabled_snapshot = ?, updated_at = ? WHERE id = ?`,
				active, now.Unix(), client.clientID); err != nil {
				return fmt.Errorf("update subscriber client effective state: %w", err)
			}
			affected[client.serverID] = struct{}{}
		}
	}
	for proxyID, target := range required {
		if _, exists := existing[proxyID]; exists {
			continue
		}
		clientID, serverID, err := s.proxies.CreateSubscriberClientTx(
			ctx, tx, userID, proxyID, subscriberClientName(username, target.name), active, now,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_clients
			(user_id, proxy_id, client_id, created_at) VALUES (?, ?, ?, ?)`,
			userID, proxyID, clientID, now.Unix()); err != nil {
			return fmt.Errorf("map subscriber client: %w", err)
		}
		affected[serverID] = struct{}{}
	}
	return nil
}

func bumpAffectedServers(ctx context.Context, tx *sql.Tx, affected map[int64]struct{}, now time.Time) ([]proxystore.Mutation, error) {
	serverIDs := make([]int64, 0, len(affected))
	for serverID := range affected {
		serverIDs = append(serverIDs, serverID)
	}
	return proxystore.BumpServerVersionsTx(ctx, tx, serverIDs, now)
}

func subscriberClientName(username, proxyName string) string {
	value := "sub-" + strings.TrimSpace(username) + "-" + strings.TrimSpace(proxyName)
	if utf8.RuneCountInString(value) <= maxNameRunes {
		return value
	}
	return string([]rune(value)[:maxNameRunes])
}

const subscriberSelect = `SELECT users.id, users.username, profiles.plan_id, plans.name,
	COALESCE(NULLIF(TRIM(plans.subscription_title), ''), plans.name, ''),
	COALESCE(plans.enabled, 0), profiles.enabled, profiles.expires_at, profiles.subscription_token,
	(SELECT COUNT(*) FROM subscriber_clients WHERE user_id = users.id),
	(SELECT COUNT(*) FROM subscription_plan_nodes AS mapping
	 JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
	 WHERE mapping.plan_id = profiles.plan_id AND nodes.enabled = 1),
	plans.traffic_limit_bytes, COALESCE(plans.traffic_reset_mode, 'never'),
	COALESCE(plans.traffic_reset_day, 1), COALESCE(plans.traffic_reset_time, '00:00'),
	plans.billing_period_months, usage.archived_uplink_bytes, usage.archived_downlink_bytes,
	usage.cycle_started_at,
	profiles.created_at, profiles.updated_at
	FROM users JOIN subscriber_profiles AS profiles ON profiles.user_id = users.id
	JOIN subscriber_usage AS usage ON usage.user_id = users.id
	LEFT JOIN subscription_plans AS plans ON plans.id = profiles.plan_id `

func scanSubscriber(row rowScanner, now time.Time) (Subscriber, error) {
	var value Subscriber
	var planID, expiresAt, trafficLimit, billingPeriod sql.NullInt64
	var planName sql.NullString
	var planEnabled, profileEnabled int
	var resetMode, resetTime string
	var resetDay int
	var archivedUplink, archivedDownlink, cycleStartedAt int64
	var createdAt, updatedAt int64
	err := row.Scan(&value.UserID, &value.Username, &planID, &planName, &value.SubscriptionTitle, &planEnabled,
		&profileEnabled, &expiresAt, &value.SubscriptionToken, &value.ClientCount,
		&value.EnabledNodeCount, &trafficLimit, &resetMode, &resetDay, &resetTime,
		&billingPeriod, &archivedUplink, &archivedDownlink, &cycleStartedAt,
		&createdAt, &updatedAt)
	if err != nil {
		return Subscriber{}, err
	}
	value.PlanID = nullableInt64Value(planID)
	value.PlanName = planName.String
	value.PlanEnabled = planEnabled != 0
	value.ProfileEnabled = profileEnabled != 0
	value.ExpiresAt = nullableTimeValue(expiresAt)
	if trafficLimit.Valid {
		limit := trafficLimit.Int64
		value.TrafficLimitBytes = &limit
	}
	if billingPeriod.Valid {
		months := int(billingPeriod.Int64)
		value.BillingPeriodMonths = &months
	}
	value.UsedBytes = saturatingAdd(archivedUplink, archivedDownlink)
	value.CycleStartedAt = time.Unix(cycleStartedAt, 0).UTC()
	value.NextResetAt, err = nextSubscriberReset(now, resetMode, resetDay, resetTime)
	if err != nil {
		return Subscriber{}, err
	}
	setSubscriberStatus(&value, now)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (s *Service) loadSubscriberUsage(ctx context.Context, value *Subscriber) error {
	upload, download, err := s.subscriberUsageBreakdown(ctx, value.UserID)
	if err != nil {
		return err
	}
	value.UsedBytes = saturatingAdd(upload, download)
	setSubscriberStatus(value, s.now().UTC())
	return nil
}

func setSubscriberStatus(value *Subscriber, now time.Time) {
	value.Active = false
	expired := value.ExpiresAt != nil && !now.UTC().Before(value.ExpiresAt.UTC())
	exhausted := value.TrafficLimitBytes != nil && value.UsedBytes >= *value.TrafficLimitBytes
	value.Status = SubscriberStatusNormal
	switch {
	case !value.ProfileEnabled:
		value.Status = SubscriberStatusDisabled
	case value.PlanID == nil:
		value.Status = SubscriberStatusUnconfigured
	case !value.PlanEnabled:
		value.Status = SubscriberStatusPlanDisabled
	case expired:
		value.Status = SubscriberStatusExpired
	case exhausted:
		value.Status = SubscriberStatusExhausted
	default:
		value.Active = true
	}
}

func nullableInt64Value(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func normalizeExpiration(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC().Truncate(time.Second)
	return &result
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func nullableTimeValue(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}
