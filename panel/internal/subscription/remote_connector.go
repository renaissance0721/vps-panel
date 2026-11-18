package subscription

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	"github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

var (
	ErrLocalConnectorNotFound = errors.New("本地连接器不存在")
	ErrLocalConnectorInvalid  = errors.New("请选择自己的已配对连接器")
	ErrLocalConnectorAuth     = errors.New("连接器凭据无效、已过期或已撤销")
	ErrLocalConnectorOffline  = errors.New("本地连接器离线，请在 Windows 上启动连接器后重试")
	ErrLocalConnectorTimeout  = errors.New("本地连接器刷新超时，请检查 Windows 网络并重试")
	errLocalFetch             = errors.New("Windows 无法下载订阅，请检查直连规则、DNS 和国内出口")
	errLocalStatus            = errors.New("订阅提供方拒绝了 Windows 请求，请检查更新开关和国内出口")
)

const (
	connectorPairLifetime    = 10 * time.Minute
	connectorTaskTimeout     = 40 * time.Second
	connectorHeartbeatExpiry = 20 * time.Second
	maxConnectorsPerOwner    = 16
)

type RemoteConnector struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	LastHeartbeat *time.Time `json:"last_heartbeat"`
	PairExpiresAt *time.Time `json:"pair_expires_at"`
}

type remoteConnectorJob struct {
	id     string
	ctx    context.Context
	result chan localconnector.Result
}

type remoteConnectorConnection struct {
	ctx           context.Context
	cancel        context.CancelFunc
	socket        *websocket.Conn
	lastHeartbeat time.Time
	job           *remoteConnectorJob
}

func (s *RemoteService) ListConnectors(ctx context.Context, actor PersonalSubscriptionActor) ([]RemoteConnector, error) {
	if validatePersonalActor(actor) != nil {
		return nil, ErrLocalConnectorNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,pair_expires_at FROM remote_subscription_connectors
		WHERE owner_user_id=? AND (pair_expires_at IS NULL OR pair_expires_at>?) ORDER BY id`, actor.UserID, s.now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []RemoteConnector{}
	for rows.Next() {
		var value RemoteConnector
		var expires sql.NullInt64
		if err := rows.Scan(&value.ID, &value.Name, &expires); err != nil {
			return nil, err
		}
		value.Status = "offline"
		if expires.Valid {
			t := time.Unix(expires.Int64, 0).UTC()
			value.PairExpiresAt, value.Status = &t, "pairing"
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	for i := range values {
		if connection := s.connectors[values[i].ID]; s.connectorOnline(connection) {
			heartbeat := connection.lastHeartbeat.UTC()
			values[i].Status, values[i].LastHeartbeat = "online", &heartbeat
		}
	}
	return values, nil
}

func (s *RemoteService) CreateConnectorPairing(ctx context.Context, actor PersonalSubscriptionActor, name string) (RemoteConnector, string, error) {
	if validatePersonalActor(actor) != nil {
		return RemoteConnector{}, "", ErrLocalConnectorNotFound
	}
	if !remoteDisplayName(name) || safeRemoteName(name) != name {
		return RemoteConnector{}, "", ErrRemoteInvalid
	}
	code, hash, err := token.New()
	if err != nil {
		return RemoteConnector{}, "", err
	}
	now := s.now().UTC().Truncate(time.Second)
	expires := now.Add(connectorPairLifetime)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteConnector{}, "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM remote_subscription_connectors WHERE owner_user_id=? AND pair_expires_at<=?`, actor.UserID, now.Unix()); err != nil {
		return RemoteConnector{}, "", err
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO remote_subscription_connectors(owner_user_id,name,token_hash,pair_expires_at)
		SELECT ?,?,?,? WHERE (SELECT COUNT(*) FROM remote_subscription_connectors WHERE owner_user_id=?)<?`,
		actor.UserID, name, hash, expires.Unix(), actor.UserID, maxConnectorsPerOwner)
	if err != nil {
		return RemoteConnector{}, "", err
	}
	if n, _ := inserted.RowsAffected(); n != 1 {
		return RemoteConnector{}, "", errors.New("本地连接器数量达到上限")
	}
	id, err := inserted.LastInsertId()
	if err != nil {
		return RemoteConnector{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return RemoteConnector{}, "", err
	}
	return RemoteConnector{ID: id, Name: name, Status: "pairing", PairExpiresAt: &expires}, code, nil
}

func (s *RemoteService) PairConnector(ctx context.Context, code string) (string, error) {
	if !localconnector.ValidToken(code) {
		return "", ErrLocalConnectorAuth
	}
	credential, hash, err := token.New()
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE remote_subscription_connectors SET token_hash=?,pair_expires_at=NULL
		WHERE token_hash=? AND pair_expires_at>? AND EXISTS (
			SELECT 1 FROM users WHERE id=owner_user_id AND role IN ('admin','vip'))`, hash, token.Hash(code), s.now().Unix())
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "", ErrLocalConnectorAuth
	}
	return credential, nil
}

func (s *RemoteService) AuthenticateConnector(ctx context.Context, credential string) (int64, error) {
	if !localconnector.ValidToken(credential) {
		return 0, ErrLocalConnectorAuth
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT c.id FROM remote_subscription_connectors c JOIN users u ON u.id=c.owner_user_id
		WHERE c.token_hash=? AND c.pair_expires_at IS NULL AND u.role IN ('admin','vip')`, token.Hash(credential)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrLocalConnectorAuth
	}
	return id, err
}

func (s *RemoteService) RevokeConnector(ctx context.Context, actor PersonalSubscriptionActor, id int64) error {
	if validatePersonalActor(actor) != nil {
		return ErrLocalConnectorNotFound
	}
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE remote_subscription_sources SET revision=revision+1,
		last_error=CASE WHEN refresh_claim<>'' THEN '本地连接器已撤销，请重新选择' ELSE last_error END
		WHERE owner_user_id=? AND connector_id=?`, actor.UserID, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM remote_subscription_connectors WHERE owner_user_id=? AND id=?`, actor.UserID, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrLocalConnectorNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if connection := s.connectors[id]; connection != nil {
		connection.cancel()
		delete(s.connectors, id)
	}
	return nil
}

// Callers serialize assignment changes with revocation under connectorMu.
func (s *RemoteService) validateConnectorAssignment(ctx context.Context, ownerID int64, mode, method string, id *int64) error {
	switch method {
	case remoteRefreshServer:
		if id == nil {
			return nil
		}
		return ErrRemoteInvalid
	case RemoteRefreshLocal:
		if mode != "url" || id == nil || *id <= 0 {
			return ErrLocalConnectorInvalid
		}
	default:
		return ErrRemoteInvalid
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM remote_subscription_connectors
		WHERE id=? AND owner_user_id=? AND pair_expires_at IS NULL)`, *id, ownerID).Scan(&exists)
	if err == nil && !exists {
		return ErrLocalConnectorInvalid
	}
	return err
}

func (s *RemoteService) connectorOnline(c *remoteConnectorConnection) bool {
	return c != nil && c.ctx.Err() == nil && time.Since(c.lastHeartbeat) < connectorHeartbeatExpiry
}

func (s *RemoteService) checkConnectorAvailable(id int64) error {
	if id <= 0 {
		return ErrLocalConnectorInvalid
	}
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	c := s.connectors[id]
	if !s.connectorOnline(c) {
		return ErrLocalConnectorOffline
	}
	if c.job != nil {
		return ErrRemoteBusy
	}
	return nil
}

func (s *RemoteService) ServeConnector(ctx context.Context, credential string, socket *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &remoteConnectorConnection{ctx: ctx, cancel: cancel, socket: socket, lastHeartbeat: time.Now()}
	s.connectorMu.Lock()
	// Recheck after the upgrade, serialized with revocation.
	id, err := s.AuthenticateConnector(ctx, credential)
	if err != nil || s.stopping {
		s.connectorMu.Unlock()
		return
	}
	if old := s.connectors[id]; old != nil {
		old.cancel()
	}
	s.connectors[id] = c
	s.connectorMu.Unlock()
	defer func() {
		s.connectorMu.Lock()
		if s.connectors[id] == c {
			delete(s.connectors, id)
		}
		s.connectorMu.Unlock()
	}()
	socket.SetReadLimit(localconnector.ResultReadLimit)
	go func() {
		defer cancel()
		localconnector.KeepAlive(ctx, socket, func() {
			s.connectorMu.Lock()
			c.lastHeartbeat = time.Now()
			s.connectorMu.Unlock()
		})
	}()
	for {
		var result localconnector.Result
		if wsjson.Read(ctx, socket, &result) != nil || !s.acceptConnectorResult(id, c, result) {
			return
		}
	}
}

func (s *RemoteService) acceptConnectorResult(id int64, c *remoteConnectorConnection, result localconnector.Result) bool {
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	if s.connectors[id] != c || c.ctx.Err() != nil || c.job == nil || c.job.id != result.ID || c.job.ctx.Err() != nil {
		return false
	}
	c.job.result <- result
	c.job = nil
	return true
}

func (s *RemoteService) connectorDispatchAllowed(ctx context.Context, actor PersonalSubscriptionActor, id int64, request remoteRefreshRequest) error {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM remote_subscription_sources s
		JOIN remote_subscription_connectors c ON c.id=s.connector_id AND c.owner_user_id=s.owner_user_id
		JOIN users u ON u.id=s.owner_user_id WHERE s.id=? AND s.owner_user_id=? AND s.revision=?
		AND s.refresh_method='local_connector' AND c.id=? AND c.pair_expires_at IS NULL AND u.role IN ('admin','vip'))`,
		id, actor.UserID, request.revision, request.connectorID).Scan(&allowed)
	if err == nil && !allowed {
		return ErrRemoteChanged
	}
	return err
}

func (s *RemoteService) connectorRoundTrip(ctx context.Context, actor PersonalSubscriptionActor, id int64, request remoteRefreshRequest) (subscriptionfetch.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, connectorTaskTimeout)
	defer cancel()
	taskID, _, err := token.New()
	if err != nil {
		return subscriptionfetch.Result{}, err
	}
	job := &remoteConnectorJob{id: taskID, ctx: ctx, result: make(chan localconnector.Result, 1)}
	s.connectorMu.Lock()
	c := s.connectors[request.connectorID]
	switch {
	case !s.connectorOnline(c):
		err = ErrLocalConnectorOffline
	case c.job != nil:
		err = ErrRemoteBusy
	default:
		err = s.connectorDispatchAllowed(ctx, actor, id, request)
	}
	if err != nil {
		s.connectorMu.Unlock()
		return subscriptionfetch.Result{}, err
	}
	c.job = job
	s.connectorMu.Unlock()
	defer func() {
		s.connectorMu.Lock()
		if c.job == job {
			// In-flight cancellations also interrupt the fetch on the client.
			c.cancel()
			c.job = nil
		}
		s.connectorMu.Unlock()
	}()

	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	stop := context.AfterFunc(c.ctx, cancelWrite)
	err = wsjson.Write(writeCtx, c.socket, localconnector.Task{ID: taskID, URL: request.address, UserAgent: request.userAgent})
	stop()
	cancelWrite()
	if err != nil {
		return subscriptionfetch.Result{}, ErrLocalConnectorOffline
	}
	var result localconnector.Result
	select {
	case <-ctx.Done():
		return subscriptionfetch.Result{}, ErrLocalConnectorTimeout
	case <-c.ctx.Done():
		select {
		case result = <-job.result:
		default:
			return subscriptionfetch.Result{}, ErrLocalConnectorOffline
		}
	case result = <-job.result:
	}
	if ctx.Err() != nil {
		return subscriptionfetch.Result{}, ErrLocalConnectorTimeout
	}
	if err := s.connectorDispatchAllowed(ctx, actor, id, request); err != nil {
		return subscriptionfetch.Result{}, err
	}
	return validateLocalResult(result)
}

func validateLocalResult(result localconnector.Result) (subscriptionfetch.Result, error) {
	if len(result.Body) > subscriptionfetch.MaxBytes || len(result.SubscriptionInfo) > localconnector.MetadataLimit {
		return subscriptionfetch.Result{}, errRemoteTooLarge
	}
	switch result.Error {
	case "":
		if result.Status != http.StatusOK {
			return subscriptionfetch.Result{}, errLocalStatus
		}
		return subscriptionfetch.Result{Status: result.Status, Body: result.Body, SubscriptionInfo: result.SubscriptionInfo}, nil
	case localconnector.Blocked:
		return subscriptionfetch.Result{}, errRemoteURL
	case localconnector.Unreachable:
		return subscriptionfetch.Result{}, errLocalFetch
	case localconnector.Timeout:
		return subscriptionfetch.Result{}, ErrLocalConnectorTimeout
	case localconnector.HTTPError:
		return subscriptionfetch.Result{}, errLocalStatus
	case localconnector.TooLarge:
		return subscriptionfetch.Result{}, errRemoteTooLarge
	default:
		return subscriptionfetch.Result{}, errRemoteFetch
	}
}

// The source's attempt, lease, success time and error are also the polling
// status. Only the active WebSocket holds the short-lived request and result.
func (s *RemoteService) StartLocalRefresh(ctx context.Context, actor PersonalSubscriptionActor, id int64) error {
	s.connectorMu.Lock()
	if s.stopping {
		s.connectorMu.Unlock()
		return ErrLocalConnectorOffline
	}
	s.localJobs.Add(1)
	s.connectorMu.Unlock()
	request, finish, err := s.beginRemoteRefresh(ctx, actor, id, RemoteRefreshLocal)
	if err != nil {
		s.localJobs.Done()
		return err
	}
	go func() {
		defer s.localJobs.Done()
		defer finish()
		jobCtx, cancel := context.WithTimeout(context.Background(), remoteRefreshTimeout)
		defer cancel()
		fetched, err := s.connectorRoundTrip(jobCtx, actor, id, request)
		_, _ = s.applyRemoteRefresh(jobCtx, actor, id, request, fetched, err)
	}()
	return nil
}

func (s *RemoteService) testLocalConnector(ctx context.Context, actor PersonalSubscriptionActor, id int64, request remoteRefreshRequest) (RemotePreview, error) {
	if err := s.checkConnectorAvailable(request.connectorID); err != nil {
		return RemotePreview{}, err
	}
	if err := s.beginRemotePreview(actor.UserID); err != nil {
		return RemotePreview{}, err
	}
	defer func() { <-s.semaphore }()
	fetched, err := s.connectorRoundTrip(ctx, actor, id, request)
	if err != nil {
		return RemotePreview{}, err
	}
	return previewRemoteFetch(fetched, request.address)
}

func (s *RemoteService) stopLocalConnectors() {
	s.connectorMu.Lock()
	s.stopping = true
	for _, c := range s.connectors {
		c.cancel()
	}
	s.connectorMu.Unlock()
	s.localJobs.Wait()
}
