package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func (s *Service) GenerateSubscription(ctx context.Context, tokenValue string) (GeneratedSubscription, []proxystore.Mutation, error) {
	data, mutations, err := s.GenerateSubscriptionData(ctx, tokenValue)
	if err != nil {
		return GeneratedSubscription{}, mutations, err
	}
	return GeneratedSubscription{
		Title: data.Title, Body: RenderBase64Subscription(data),
		Upload: data.Upload, Download: data.Download, Total: data.Total, Expire: data.Expire,
	}, mutations, nil
}

func (s *Service) GenerateSubscriptionData(ctx context.Context, tokenValue string) (SubscriptionData, []proxystore.Mutation, error) {
	if tokenValue == "" {
		return SubscriptionData{}, nil, ErrSubscriptionNotFound
	}
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT profiles.user_id
		FROM subscriber_profiles AS profiles JOIN users ON users.id = profiles.user_id
		WHERE profiles.subscription_token = ? AND users.role = 'subscriber'`, tokenValue).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionData{}, nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return SubscriptionData{}, nil, fmt.Errorf("find subscription token: %w", err)
	}
	mutations, err := s.ReconcileSubscriber(ctx, userID)
	if err != nil {
		return SubscriptionData{}, nil, err
	}
	subscriber, err := s.GetSubscriber(ctx, userID)
	if err != nil {
		return SubscriptionData{}, nil, err
	}
	if !subscriber.Active {
		return SubscriptionData{}, mutations, ErrSubscriptionUnavailable
	}

	type subscriptionNode struct {
		name          string
		mode          string
		entryHostMode string
		multiplierBP  int
		clientID      int64
		entryAddress  string
		entryPort     int
	}
	rows, err := s.db.QueryContext(ctx, `SELECT nodes.name, nodes.mode, nodes.entry_host_mode,
		nodes.traffic_multiplier_bp, clients.client_id,
		CASE WHEN nodes.entry_host_mode = 'manual' THEN nodes.entry_host
		     WHEN nodes.mode = 'relay' THEN COALESCE(source_info.public_ipv4, '')
		     ELSE '' END,
		CASE WHEN nodes.mode = 'relay' THEN COALESCE(relay.listen_port, 0)
		     ELSE target.listen_port END
		FROM subscriber_profiles AS profiles
		JOIN subscription_plan_nodes AS mapping ON mapping.plan_id = profiles.plan_id
		JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
		JOIN subscriber_clients AS clients ON clients.user_id = profiles.user_id
		 AND clients.proxy_id = nodes.target_proxy_id
		JOIN proxies AS target ON target.id = nodes.target_proxy_id
		LEFT JOIN relays AS relay ON relay.id = nodes.relay_id
		LEFT JOIN server_system_info AS source_info ON source_info.server_id = relay.server_id
		WHERE profiles.user_id = ? AND nodes.enabled = 1
		ORDER BY mapping.position`, userID)
	if err != nil {
		return SubscriptionData{}, mutations, fmt.Errorf("list subscription nodes: %w", err)
	}
	nodes := make([]subscriptionNode, 0)
	for rows.Next() {
		var value subscriptionNode
		if err := rows.Scan(&value.name, &value.mode, &value.entryHostMode, &value.multiplierBP,
			&value.clientID, &value.entryAddress, &value.entryPort); err != nil {
			rows.Close()
			return SubscriptionData{}, mutations, fmt.Errorf("scan subscription node: %w", err)
		}
		nodes = append(nodes, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SubscriptionData{}, mutations, fmt.Errorf("iterate subscription nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return SubscriptionData{}, mutations, fmt.Errorf("close subscription nodes: %w", err)
	}
	if len(nodes) == 0 {
		return SubscriptionData{}, mutations, ErrSubscriptionUnavailable
	}

	shares := make([]proxystore.ClientShare, 0, len(nodes))
	for _, node := range nodes {
		options := proxystore.ShareOptions{DisplayName: FormatNodeDisplayName(node.name, node.multiplierBP)}
		var share proxystore.ClientShare
		if node.mode == NodeModeDirect && node.entryHostMode == EntryHostModeInherit {
			share, err = s.proxies.GetClientShareWithOptions(ctx, node.clientID, options)
		} else if (node.mode == NodeModeDirect || node.mode == NodeModeRelay) && node.entryAddress != "" && node.entryPort > 0 {
			share, err = s.proxies.GetClientShareAtEndpointWithOptions(ctx, node.clientID, proxystore.ShareEndpoint{
				Address: node.entryAddress, Port: node.entryPort,
			}, options)
		} else {
			return SubscriptionData{}, mutations, ErrSubscriptionUnavailable
		}
		if err != nil {
			return SubscriptionData{}, mutations, fmt.Errorf("build subscription node share: %w", err)
		}
		shares = append(shares, share)
	}
	upload, download, err := s.subscriberUsageBreakdown(ctx, userID)
	if err != nil {
		return SubscriptionData{}, mutations, err
	}
	result := SubscriptionData{
		Title: subscriber.SubscriptionTitle, Nodes: shares, Upload: upload, Download: download,
	}
	if subscriber.TrafficLimitBytes != nil {
		result.Total = *subscriber.TrafficLimitBytes
	}
	if subscriber.ExpiresAt != nil {
		result.Expire = subscriber.ExpiresAt.Unix()
	}
	return result, mutations, nil
}

func (s *Service) subscriberUsageBreakdown(ctx context.Context, userID int64) (int64, int64, error) {
	var upload, download int64
	if err := s.db.QueryRowContext(ctx, `SELECT archived_uplink_bytes, archived_downlink_bytes
		FROM subscriber_usage WHERE user_id = ?`, userID).Scan(&upload, &download); errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrSubscriberNotFound
	} else if err != nil {
		return 0, 0, fmt.Errorf("read subscriber archived usage: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT mapping.charged_uplink_bytes,
		mapping.charged_downlink_bytes
		FROM subscriber_clients AS mapping
		WHERE mapping.user_id = ?`, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("list subscription userinfo usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var currentUpload, currentDownload int64
		if err := rows.Scan(&currentUpload, &currentDownload); err != nil {
			return 0, 0, fmt.Errorf("scan subscription userinfo usage: %w", err)
		}
		upload = saturatingAdd(upload, currentUpload)
		download = saturatingAdd(download, currentDownload)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate subscription userinfo usage: %w", err)
	}
	return upload, download, nil
}
