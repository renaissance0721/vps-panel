package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func (s *Service) RegisterAgent(
	ctx context.Context,
	enrollmentToken string,
	agentVersion string,
	existingConfig bool,
) (RegisteredAgent, error) {
	return s.RegisterAgentWithMetadata(ctx, enrollmentToken, Metadata{Version: agentVersion}, existingConfig)
}

func (s *Service) RegisterAgentWithMetadata(
	ctx context.Context,
	enrollmentToken string,
	metadata Metadata,
	_ bool,
) (RegisteredAgent, error) {
	if enrollmentToken == "" {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	metadata, err := NormalizeMetadata(metadata)
	if err != nil {
		return RegisteredAgent{}, err
	}
	if metadata.Version == "" {
		return RegisteredAgent{}, ErrInvalidAgentVersion
	}
	capabilitiesJSON, err := EncodeCapabilities(metadata.Capabilities)
	if err != nil {
		return RegisteredAgent{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("begin agent registration: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second)
	var enrollmentID, serverID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, server_id FROM agent_enrollments
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(enrollmentToken), now.Unix(),
	).Scan(&enrollmentID, &serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("find agent enrollment: %w", err)
	}
	agentToken, agentTokenHash, err := token.New()
	if err != nil {
		return RegisteredAgent{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, serverID); err != nil {
		return RegisteredAgent{}, fmt.Errorf("replace previous Agent: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO agents
		 (server_id, token_hash, implementation, version, api_version, capabilities_json,
		  registered_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, agentTokenHash, metadata.Implementation, metadata.Version, metadata.APIVersion,
		capabilitiesJSON, now.Unix(), now.Unix(), now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("create agent: %w", err)
	}
	agentID, err := result.LastInsertId()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read agent id: %w", err)
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET used_at = ?
		 WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), enrollmentID, now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("use agent enrollment: %w", err)
	}
	usedCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read used enrollment count: %w", err)
	}
	if usedCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = NULL, updated_at = ? WHERE id = ?`,
		statusOffline, now.Unix(), serverID,
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("update registered server: %w", err)
	}
	serverCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read updated server count: %w", err)
	}
	if serverCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	if err := tx.Commit(); err != nil {
		return RegisteredAgent{}, fmt.Errorf("commit agent registration: %w", err)
	}
	return RegisteredAgent{
		ID:       agentID,
		ServerID: serverID,
		Token:    agentToken,
	}, nil
}

func (s *Service) AuthenticateAgent(ctx context.Context, agentToken string) (Agent, error) {
	if agentToken == "" {
		return Agent{}, ErrInvalidAgentToken
	}

	var agent Agent
	err := s.db.QueryRowContext(ctx,
		`SELECT agents.id, agents.server_id, agents.implementation FROM agents
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.token_hash = ? AND servers.archived_at IS NULL`, token.Hash(agentToken),
	).Scan(&agent.ID, &agent.ServerID, &agent.Implementation)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrInvalidAgentToken
	}
	if err != nil {
		return Agent{}, fmt.Errorf("authenticate agent: %w", err)
	}
	return agent, nil
}
