package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type personalRemoteNode struct {
	id                              int64
	name, protocol, format, payload string
}

// Read only cached data and always check the owning user at the query boundary.
func (s *Service) personalRemoteNodes(ctx context.Context, ownerID, sourceID, nodeID int64, payload bool) ([]personalRemoteNode, error) {
	query := `SELECT n.id,n.display_name,n.protocol,n.format,n.payload_ciphertext FROM remote_subscription_nodes n
		JOIN remote_subscription_sources s ON s.id=n.source_id WHERE s.owner_user_id=? AND s.enabled=1`
	args := []any{ownerID}
	if sourceID > 0 {
		query += ` AND s.id=?`
		args = append(args, sourceID)
	}
	if nodeID > 0 {
		query += ` AND n.id=?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY n.position,n.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var values []personalRemoteNode
	for rows.Next() {
		var n personalRemoteNode
		if err := rows.Scan(&n.id, &n.name, &n.protocol, &n.format, &n.payload); err != nil {
			rows.Close()
			return nil, err
		}
		values = append(values, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return values, nil
	}
	if s.remoteSecrets == nil {
		return nil, ErrRemoteKey
	}
	ciphertexts := make([]string, 0, len(values)*2)
	for _, value := range values {
		ciphertexts = append(ciphertexts, value.name)
		if payload {
			ciphertexts = append(ciphertexts, value.payload)
		}
	}
	decrypted, err := s.remoteSecrets.DecryptMany(ciphertexts)
	if err != nil {
		return nil, ErrRemoteKey
	}
	for i := range values {
		index := i
		if payload {
			index = 2 * i
		}
		values[i].name = safeRemoteName(decrypted[index])
		if payload {
			values[i].payload = decrypted[index+1]
		} else {
			values[i].payload = ""
		}
	}
	return values, nil
}

func (s *Service) inspectPersonalRemote(ctx context.Context, actor PersonalSubscriptionActor, sourceType string, id int64) (personalSourceState, error) {
	var name, protocol string
	var enabled int
	var err error
	if sourceType == PersonalSourceRemoteSource {
		err = s.db.QueryRowContext(ctx, `SELECT name,enabled FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&name, &enabled)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT s.name,s.enabled,n.protocol FROM remote_subscription_nodes n JOIN remote_subscription_sources s ON s.id=n.source_id WHERE n.id=? AND s.owner_user_id=?`, id, actor.UserID).Scan(&name, &enabled, &protocol)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return unavailablePersonalSource("机场订阅", PersonalNodeUnavailable, "来源不存在或不可访问"), nil
	}
	if err != nil {
		return personalSourceState{}, err
	}
	state := personalSourceState{name: name, detail: name, accessible: true, selectable: enabled != 0, status: PersonalNodeReady, statusDetail: "使用本地缓存"}
	if protocol != "" {
		state.detail = fmt.Sprintf("%s · %s", name, protocol)
	}
	if enabled == 0 {
		state.status, state.statusDetail = PersonalNodeSourceDisabled, "机场订阅已停用"
	}
	return state, nil
}

func (s *Service) listPersonalRemoteSources(ctx context.Context, actor PersonalSubscriptionActor) ([]PersonalSubscriptionSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name FROM remote_subscription_sources WHERE owner_user_id=? AND enabled=1 ORDER BY id`, actor.UserID)
	if err != nil {
		return nil, err
	}
	type source struct {
		id   int64
		name string
	}
	var sources []source
	for rows.Next() {
		var x source
		if err := rows.Scan(&x.id, &x.name); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var result []PersonalSubscriptionSource
	for _, source := range sources {
		result = append(result, PersonalSubscriptionSource{SourceType: PersonalSourceRemoteSource, SourceID: source.id, Name: source.name, Detail: "机场订阅 · 自动包含缓存节点", DefaultName: source.name, Status: PersonalNodeReady, StatusDetail: "使用本地缓存"})
		nodes, err := s.personalRemoteNodes(ctx, actor.UserID, source.id, 0, false)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			result = append(result, PersonalSubscriptionSource{SourceType: PersonalSourceRemoteNode, SourceID: n.id, Name: n.name, Detail: source.name + " · " + n.protocol, DefaultName: n.name, Status: PersonalNodeReady, StatusDetail: "使用本地缓存"})
		}
	}
	return result, nil
}
