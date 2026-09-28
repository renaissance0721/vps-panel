package subscription

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func (s *Service) GenerateSubscription(ctx context.Context, tokenValue string) (GeneratedSubscription, []proxystore.Mutation, error) {
	if tokenValue == "" {
		return GeneratedSubscription{}, nil, ErrSubscriptionNotFound
	}
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT profiles.user_id
		FROM subscriber_profiles AS profiles JOIN users ON users.id = profiles.user_id
		WHERE profiles.subscription_token = ? AND users.role = 'subscriber'`, tokenValue).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return GeneratedSubscription{}, nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return GeneratedSubscription{}, nil, fmt.Errorf("find subscription token: %w", err)
	}
	mutations, err := s.ReconcileSubscriber(ctx, userID)
	if err != nil {
		return GeneratedSubscription{}, nil, err
	}
	subscriber, err := s.GetSubscriber(ctx, userID)
	if err != nil {
		return GeneratedSubscription{}, nil, err
	}
	if !subscriber.Active {
		return GeneratedSubscription{}, mutations, ErrSubscriptionUnavailable
	}

	type subscriptionNode struct {
		name         string
		mode         string
		clientID     int64
		entryAddress string
		entryPort    int
	}
	rows, err := s.db.QueryContext(ctx, `SELECT nodes.name, nodes.mode, clients.client_id,
		CASE WHEN relay.entry_host_mode = 'manual' THEN relay.entry_host
		     ELSE COALESCE(source_info.public_ipv4, '') END,
		COALESCE(relay.listen_port, 0)
		FROM subscriber_profiles AS profiles
		JOIN subscription_plan_nodes AS mapping ON mapping.plan_id = profiles.plan_id
		JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
		JOIN subscriber_clients AS clients ON clients.user_id = profiles.user_id
		 AND clients.proxy_id = nodes.target_proxy_id
		LEFT JOIN relays AS relay ON relay.id = nodes.relay_id
		LEFT JOIN server_system_info AS source_info ON source_info.server_id = relay.server_id
		WHERE profiles.user_id = ? AND nodes.enabled = 1
		ORDER BY mapping.position`, userID)
	if err != nil {
		return GeneratedSubscription{}, mutations, fmt.Errorf("list subscription nodes: %w", err)
	}
	nodes := make([]subscriptionNode, 0)
	for rows.Next() {
		var value subscriptionNode
		if err := rows.Scan(&value.name, &value.mode, &value.clientID, &value.entryAddress, &value.entryPort); err != nil {
			rows.Close()
			return GeneratedSubscription{}, mutations, fmt.Errorf("scan subscription node: %w", err)
		}
		nodes = append(nodes, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return GeneratedSubscription{}, mutations, fmt.Errorf("iterate subscription nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return GeneratedSubscription{}, mutations, fmt.Errorf("close subscription nodes: %w", err)
	}

	URIs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		options := proxystore.ShareOptions{DisplayName: node.name}
		var share proxystore.ClientShare
		if node.mode == NodeModeDirect {
			share, err = s.proxies.GetClientShareWithOptions(ctx, node.clientID, options)
		} else if node.mode == NodeModeRelay && node.entryAddress != "" && node.entryPort > 0 {
			share, err = s.proxies.GetClientShareAtEndpointWithOptions(ctx, node.clientID, proxystore.ShareEndpoint{
				Address: node.entryAddress, Port: node.entryPort,
			}, options)
		} else {
			return GeneratedSubscription{}, mutations, ErrSubscriptionUnavailable
		}
		if err != nil {
			return GeneratedSubscription{}, mutations, fmt.Errorf("build subscription node share: %w", err)
		}
		URIs = append(URIs, share.URI)
	}
	upload, download, err := s.subscriberUsageBreakdown(ctx, userID)
	if err != nil {
		return GeneratedSubscription{}, mutations, err
	}
	result := GeneratedSubscription{
		Body:   base64.StdEncoding.EncodeToString([]byte(strings.Join(URIs, "\n"))),
		Upload: upload, Download: download,
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
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(metrics.cycle_uplink_bytes, 0),
		COALESCE(metrics.cycle_downlink_bytes, 0)
		FROM subscriber_clients AS mapping
		LEFT JOIN client_metrics AS metrics ON metrics.client_id = mapping.client_id
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
