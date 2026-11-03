package api

import (
	"context"
	"database/sql"
	"errors"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type carpoolAssignment struct {
	ID, UserID, ClientID, SourceID int64
	SourceType                     string
}

type carpoolSource struct {
	ID          int64           `json:"id,omitempty"`
	SourceType  string          `json:"source_type"`
	SourceID    int64           `json:"source_id"`
	DisplayName string          `json:"display_name"`
	ServerName  string          `json:"server_name"`
	ProxyName   string          `json:"proxy_name"`
	Protocol    string          `json:"protocol"`
	Client      *clientResponse `json:"client,omitempty"`
}

func (s *server) carpoolAssignments(ctx context.Context, userID int64) ([]carpoolAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,client_id,source_type,source_id FROM carpool_node_assignments WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []carpoolAssignment{}
	for rows.Next() {
		var v carpoolAssignment
		if err := rows.Scan(&v.ID, &v.UserID, &v.ClientID, &v.SourceType, &v.SourceID); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
func (s *server) carpoolAssignment(ctx context.Context, userID, id int64) (carpoolAssignment, error) {
	var v carpoolAssignment
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,client_id,source_type,source_id FROM carpool_node_assignments WHERE user_id = ? AND id = ?`, userID, id).Scan(&v.ID, &v.UserID, &v.ClientID, &v.SourceType, &v.SourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, proxystore.ErrClientNotFound
	}
	return v, err
}
func relayCarpoolAvailable(r relaystore.Relay, clientProxyID int64, protocol string) bool {
	return relaystore.CarpoolAvailable(r, clientProxyID, protocol)
}
func (s *server) carpoolSources(ctx context.Context) ([]carpoolSource, error) {
	proxies, err := s.proxies.ListDistributable(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]proxystore.Proxy, len(proxies))
	out := []carpoolSource{}
	for _, p := range proxies {
		byID[p.ID] = p
		out = append(out, carpoolSource{SourceType: "proxy", SourceID: p.ID, DisplayName: p.ServerName + " · " + p.Name, ServerName: p.ServerName, ProxyName: p.Name, Protocol: p.Protocol})
	}
	relays, err := s.relays.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range relays {
		if r.TargetProxyID == nil {
			continue
		}
		p, ok := byID[*r.TargetProxyID]
		if !ok || !relayCarpoolAvailable(r, p.ID, p.Protocol) {
			continue
		}
		out = append(out, carpoolSource{SourceType: "relay", SourceID: r.ID, DisplayName: r.Name, ServerName: r.ServerName, ProxyName: p.ServerName + " · " + p.Name, Protocol: p.Protocol})
	}
	return out, nil
}
func (s *server) carpoolNode(ctx context.Context, a carpoolAssignment) (carpoolSource, proxystore.Client, error) {
	client, err := s.proxies.GetAssignedClient(ctx, a.UserID, a.ClientID)
	if err != nil {
		return carpoolSource{}, client, err
	}
	n := carpoolSource{ID: a.ID, SourceType: a.SourceType, SourceID: a.SourceID, DisplayName: client.ServerName + " · " + client.ProxyName, ServerName: client.ServerName, ProxyName: client.ProxyName, Protocol: client.Protocol}
	if a.SourceType == "relay" {
		r, err := s.relays.Get(ctx, a.SourceID)
		if err != nil {
			return n, client, err
		}
		n.DisplayName = r.Name
		n.ServerName = r.ServerName
		n.ProxyName = client.ServerName + " · " + client.ProxyName
	}
	value := toClientResponse(client)
	n.Client = &value
	return n, client, nil
}

func (s *server) carpoolClientHasNode(ctx context.Context, userID int64, client proxystore.Client) (bool, error) {
	assignments, err := s.carpoolAssignments(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, a := range assignments {
		if a.ClientID != client.ID {
			continue
		}
		if a.SourceType == "proxy" && a.SourceID == client.ProxyID {
			return true, nil
		}
		if a.SourceType == "relay" {
			r, err := s.relays.Get(ctx, a.SourceID)
			if errors.Is(err, relaystore.ErrNotFound) {
				continue
			}
			if err != nil {
				return false, err
			}
			if relayCarpoolAvailable(r, client.ProxyID, client.Protocol) {
				return true, nil
			}
		}
	}
	return false, nil
}
