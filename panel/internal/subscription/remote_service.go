package subscription

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
	"gopkg.in/yaml.v3"
)

var (
	ErrRemoteNotFound = errors.New("机场订阅不存在")
	ErrRemoteInvalid  = errors.New("机场订阅设置无效")
	ErrRemoteBusy     = errors.New("机场订阅正在刷新或请求过于频繁")
	ErrRemoteKey      = errors.New("机场订阅加密密钥不可用")
	ErrRemoteChanged  = errors.New("机场订阅已修改，请重新刷新")
	ErrRemoteManual   = errors.New("手动来源没有订阅地址，请导入文件")
	ErrRemoteImport   = errors.New("文件内容无效或没有可用节点")
)

const (
	remoteMinAttempt     = 60 * time.Second
	remoteRefreshTimeout = 2 * time.Minute
	remoteRefreshLease   = 3 * time.Minute
	MaxRemoteImportBytes = remoteSubscriptionMaxBytes
)

type RemoteSource struct {
	ID              int64      `json:"id"`
	Mode            string     `json:"mode"`
	Name            string     `json:"name"`
	UserAgent       string     `json:"user_agent"`
	Enabled         bool       `json:"enabled"`
	IntervalSeconds int        `json:"interval_seconds"`
	LastAttempt     *time.Time `json:"last_attempt_at"`
	LastSuccess     *time.Time `json:"last_success_at"`
	LastError       string     `json:"last_error"`
	Warnings        []string   `json:"warnings"`
	NodeCount       int        `json:"node_count"`
	Refreshing      bool       `json:"refreshing"`
	Upload          *int64     `json:"upload"`
	Download        *int64     `json:"download"`
	Total           *int64     `json:"total"`
	Expire          *int64     `json:"expire"`
	leaseUntil      sql.NullInt64
}

type RemoteNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Format   string `json:"format"`
	Position int    `json:"position"`
}

type RemoteInput struct {
	Mode            string `json:"mode"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	UserAgent       string `json:"user_agent"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type RemotePreview struct {
	Nodes    []RemoteNode `json:"nodes"`
	Warnings []string     `json:"warnings"`
	Usable   bool         `json:"usable"`
	Upload   *int64       `json:"upload"`
	Download *int64       `json:"download"`
	Total    *int64       `json:"total"`
	Expire   *int64       `json:"expire"`
}

// RemoteService never exposes source URLs or cached payloads through its DTOs.
type RemoteService struct {
	db          *sql.DB
	secrets     *mailservice.SecretStore
	mailSecrets *mailservice.SecretStore
	fetcher     *remoteFetcher
	now         func() time.Time
	mu          sync.Mutex
	activeMu    sync.Mutex
	active      map[int64]bool
	previewLast map[int64]time.Time
	semaphore   chan struct{}
}

func NewRemoteService(db *sql.DB, keyPath string) *RemoteService {
	return &RemoteService{db: db, secrets: mailservice.NewRemoteSubscriptionSecretStore(keyPath),
		mailSecrets: mailservice.NewMailSecretStore(keyPath), fetcher: newRemoteFetcher(), now: time.Now,
		active: make(map[int64]bool), previewLast: make(map[int64]time.Time), semaphore: make(chan struct{}, 4)}
}

const remoteSourceSelect = `SELECT s.id,s.source_mode,s.name,s.user_agent,s.enabled,s.interval_seconds,
	s.last_attempt_at,s.last_success_at,s.last_error,s.warnings_json,
	(SELECT COUNT(*) FROM remote_subscription_nodes n WHERE n.source_id=s.id),
	s.upload,s.download,s.total,s.expire,s.refresh_lease_until FROM remote_subscription_sources s`

func scanRemoteSource(row rowScanner) (RemoteSource, error) {
	var value RemoteSource
	var attempt, success sql.NullInt64
	var warnings string
	err := row.Scan(&value.ID, &value.Mode, &value.Name, &value.UserAgent, &value.Enabled, &value.IntervalSeconds,
		&attempt, &success, &value.LastError, &warnings, &value.NodeCount,
		&value.Upload, &value.Download, &value.Total, &value.Expire, &value.leaseUntil)
	if err != nil {
		return RemoteSource{}, err
	}
	if attempt.Valid {
		t := time.Unix(attempt.Int64, 0).UTC()
		value.LastAttempt = &t
	}
	if success.Valid {
		t := time.Unix(success.Int64, 0).UTC()
		value.LastSuccess = &t
	}
	if json.Unmarshal([]byte(warnings), &value.Warnings) != nil {
		return RemoteSource{}, errors.New("invalid remote warning state")
	}
	if value.Warnings == nil {
		value.Warnings = []string{}
	}
	return value, nil
}

func (s *RemoteService) List(ctx context.Context, actor PersonalSubscriptionActor) ([]RemoteSource, error) {
	if validatePersonalActor(actor) != nil {
		return nil, ErrRemoteNotFound
	}
	rows, err := s.db.QueryContext(ctx, remoteSourceSelect+` WHERE s.owner_user_id=? ORDER BY s.id`, actor.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RemoteSource{}
	for rows.Next() {
		value, err := scanRemoteSource(rows)
		if err != nil {
			return nil, err
		}
		s.decorateStatus(&value)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *RemoteService) Get(ctx context.Context, actor PersonalSubscriptionActor, id int64) (RemoteSource, error) {
	if validatePersonalActor(actor) != nil || id <= 0 {
		return RemoteSource{}, ErrRemoteNotFound
	}
	value, err := scanRemoteSource(s.db.QueryRowContext(ctx, remoteSourceSelect+` WHERE s.owner_user_id=? AND s.id=?`, actor.UserID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteSource{}, ErrRemoteNotFound
	}
	if err == nil {
		s.decorateStatus(&value)
	}
	return value, err
}

func (s *RemoteService) decorateStatus(value *RemoteSource) {
	now := s.now().UTC().Unix()
	value.Refreshing = s.isRefreshing(value.ID) || (value.leaseUntil.Valid && value.leaseUntil.Int64 > now)
	if !value.Refreshing && value.leaseUntil.Valid && value.LastError == "" {
		if value.Mode == "manual" {
			value.LastError = "上次导入中断，请重新导入"
		} else {
			value.LastError = "上次刷新中断，等待重试"
		}
	}
}

func (s *RemoteService) isRefreshing(id int64) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.active[id]
}

func validRemoteInput(input RemoteInput, requireURL bool) bool {
	if strings.TrimSpace(input.Name) != input.Name || !remoteDisplayName(input.Name) || safeRemoteName(input.Name) != input.Name || input.IntervalSeconds < 300 || input.IntervalSeconds > 604800 || input.IntervalSeconds%60 != 0 || len(input.UserAgent) > 512 {
		return false
	}
	for _, r := range input.UserAgent {
		if unicode.IsControl(r) {
			return false
		}
	}
	if input.URL == "" {
		return !requireURL
	}
	parsed, err := url.Parse(input.URL)
	return err == nil && len(input.URL) <= 8192 && strings.TrimSpace(input.URL) == input.URL && validateRemoteURL(parsed) == nil
}

// Verify the existing key before generating any ciphertext, including when the
// existing encrypted data belongs to mail. A missing/wrong key must not be
// silently replaced while it is still needed by stored secrets.
func (s *RemoteService) checkExistingKey(ctx context.Context) (bool, error) {
	var ciphertext string
	err := s.db.QueryRowContext(ctx, `SELECT url_ciphertext FROM remote_subscription_sources LIMIT 1`).Scan(&ciphertext)
	if err == nil {
		if _, err = s.secrets.Decrypt(ciphertext); err != nil {
			return true, ErrRemoteKey
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT password_ciphertext FROM mail_settings WHERE password_ciphertext <> '' LIMIT 1`).Scan(&ciphertext)
	if err == nil {
		if _, err = s.mailSecrets.Decrypt(ciphertext); err != nil {
			return true, ErrRemoteKey
		}
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (s *RemoteService) Create(ctx context.Context, actor PersonalSubscriptionActor, input RemoteInput) (RemoteSource, error) {
	if validatePersonalActor(actor) != nil {
		return RemoteSource{}, ErrRemoteNotFound
	}
	if input.Mode == "" {
		input.Mode = "url"
	}
	if (input.Mode != "url" && input.Mode != "manual") || (input.Mode == "manual" && input.URL != "") || !validRemoteInput(input, input.Mode == "url") {
		return RemoteSource{}, ErrRemoteInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keyRequired, err := s.checkExistingKey(ctx)
	if err != nil {
		return RemoteSource{}, err
	}
	var secret string
	if keyRequired {
		secret, err = s.secrets.EncryptExisting(input.URL)
	} else {
		secret, err = s.secrets.Encrypt(input.URL)
	}
	if err != nil {
		return RemoteSource{}, ErrRemoteKey
	}
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `INSERT INTO remote_subscription_sources
		(owner_user_id,name,url_ciphertext,user_agent,enabled,interval_seconds,created_at,updated_at,source_mode)
		VALUES(?,?,?,?,?,?,?,?,?)`, actor.UserID, input.Name, secret, input.UserAgent, input.Enabled, input.IntervalSeconds, now, now, input.Mode)
	if err != nil {
		return RemoteSource{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RemoteSource{}, err
	}
	return s.Get(ctx, actor, id)
}

func (s *RemoteService) Update(ctx context.Context, actor PersonalSubscriptionActor, id int64, input RemoteInput) (RemoteSource, error) {
	current, err := s.Get(ctx, actor, id)
	if err != nil {
		return RemoteSource{}, err
	}
	if (input.Mode != "" && input.Mode != current.Mode) || (current.Mode == "manual" && input.URL != "") {
		return RemoteSource{}, ErrRemoteInvalid
	}
	if !validRemoteInput(input, false) {
		return RemoteSource{}, ErrRemoteInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var secret string
	if err := s.db.QueryRowContext(ctx, `SELECT url_ciphertext FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&secret); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RemoteSource{}, ErrRemoteNotFound
		}
		return RemoteSource{}, err
	}
	if _, err := s.secrets.Decrypt(secret); err != nil {
		return RemoteSource{}, ErrRemoteKey
	}
	if input.URL != "" {
		values, err := s.secrets.EncryptManyExistingChecked([]string{input.URL}, secret)
		if err != nil {
			return RemoteSource{}, ErrRemoteKey
		}
		secret = values[0]
	}
	result, err := s.db.ExecContext(ctx, `UPDATE remote_subscription_sources SET name=?,url_ciphertext=?,user_agent=?,enabled=?,
		interval_seconds=?,last_error=CASE WHEN ? AND cache_generation>0 THEN '订阅地址已更换，缓存仍来自上次成功刷新' WHEN ? THEN '' ELSE last_error END,
		revision=revision+1,updated_at=? WHERE id=? AND owner_user_id=?`, input.Name, secret, input.UserAgent,
		input.Enabled, input.IntervalSeconds, input.URL != "", input.URL != "", s.now().UTC().Unix(), id, actor.UserID)
	if err != nil {
		return RemoteSource{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return RemoteSource{}, ErrRemoteNotFound
	}
	return s.Get(ctx, actor, id)
}

func (s *RemoteService) Delete(ctx context.Context, actor PersonalSubscriptionActor, id int64) error {
	if validatePersonalActor(actor) != nil {
		return ErrRemoteNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrRemoteNotFound
	}
	return nil
}

func safeRemoteName(name string) string {
	lower := strings.ToLower(name)
	if !utf8.ValidString(name) || strings.Contains(name, "://") || strings.Contains(name, "@") ||
		strings.Contains(lower, "token=") || strings.Contains(lower, "password=") || strings.Contains(lower, "uuid=") {
		return "节点名称已隐藏"
	}
	return name
}

func safeRemoteNodeName(node remoteParsedNode) string {
	name := safeRemoteName(node.name)
	if name != node.name {
		return name
	}
	containsSecret := func(value string) bool { return value != "" && strings.Contains(name, value) }
	if node.format == "uri" {
		parsed, err := url.Parse(node.payload)
		if err != nil {
			return "节点名称已隐藏"
		}
		if parsed.User != nil {
			if containsSecret(parsed.User.Username()) {
				return "节点名称已隐藏"
			}
			if password, ok := parsed.User.Password(); ok && containsSecret(password) {
				return "节点名称已隐藏"
			}
		}
		if node.protocol == "ss" {
			userinfo := parsed.Host
			if parsed.User != nil {
				userinfo = parsed.User.Username()
			}
			if decoded, ok := decodeRemoteBase64(userinfo); ok {
				credentials, _, _ := strings.Cut(string(decoded), "@")
				_, password, ok := strings.Cut(credentials, ":")
				if ok && containsSecret(password) {
					return "节点名称已隐藏"
				}
			}
		}
		for key, values := range parsed.Query() {
			if remoteSecretKey(key) {
				for _, value := range values {
					if containsSecret(value) {
						return "节点名称已隐藏"
					}
				}
			}
		}
	} else {
		var doc yaml.Node
		if yaml.Unmarshal([]byte(node.payload), &doc) != nil {
			return "节点名称已隐藏"
		}
		stack := []*yaml.Node{&doc}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if current.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(current.Content); i += 2 {
					if remoteSecretKey(current.Content[i].Value) && current.Content[i+1].Kind == yaml.ScalarNode && containsSecret(current.Content[i+1].Value) {
						return "节点名称已隐藏"
					}
				}
			}
			stack = append(stack, current.Content...)
		}
	}
	return name
}

func remoteSecretKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	return strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") ||
		strings.Contains(key, "private-key") || strings.Contains(key, "credential") || key == "uuid" || key == "auth" || key == "id" || key == "psk" || key == "pass" || key == "passwd"
}

// Names are supplied by the remote server, so check them against credentials
// from the entire source, not just the node to which each name belongs.
func safeRemoteNodeNames(nodes []remoteParsedNode, address string) []string {
	secrets := map[string]struct{}{}
	add := func(value string) {
		if value != "" {
			secrets[value] = struct{}{}
		}
	}
	if source, err := url.Parse(address); err == nil {
		if source.User != nil {
			add(source.User.Username())
			if password, ok := source.User.Password(); ok {
				add(password)
			}
		}
		for _, values := range source.Query() {
			for _, value := range values {
				add(value)
			}
		}
		for _, part := range strings.Split(source.Path, "/") {
			if len(part) >= 8 {
				add(part)
			}
		}
	}
	failed := false
	for _, node := range nodes {
		if node.format == "uri" {
			parsed, err := url.Parse(node.payload)
			if err != nil {
				failed = true
				break
			}
			if parsed.User != nil {
				add(parsed.User.Username())
				if password, ok := parsed.User.Password(); ok {
					add(password)
				}
			}
			if node.protocol == "ss" {
				userinfo := parsed.Host
				if parsed.User != nil {
					userinfo = parsed.User.Username()
				}
				if decoded, ok := decodeRemoteBase64(userinfo); ok {
					credentials, _, _ := strings.Cut(string(decoded), "@")
					if _, password, ok := strings.Cut(credentials, ":"); ok {
						add(password)
					}
				}
			}
			for key, values := range parsed.Query() {
				if remoteSecretKey(key) {
					for _, value := range values {
						add(value)
					}
				}
			}
		} else {
			var doc yaml.Node
			if yaml.Unmarshal([]byte(node.payload), &doc) != nil {
				failed = true
				break
			}
			stack := []*yaml.Node{&doc}
			for len(stack) > 0 {
				current := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if current.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(current.Content); i += 2 {
						if remoteSecretKey(current.Content[i].Value) && current.Content[i+1].Kind == yaml.ScalarNode {
							add(current.Content[i+1].Value)
						}
					}
				}
				stack = append(stack, current.Content...)
			}
		}
	}
	names := make([]string, len(nodes))
	// A malicious input must not cause unbounded work in name comparisons.
	if len(nodes) > 0 && len(secrets) > 1000000/len(nodes) {
		failed = true
	}
	for i, node := range nodes {
		names[i] = safeRemoteNodeName(node)
		if failed {
			names[i] = "节点名称已隐藏"
			continue
		}
		for secret := range secrets {
			if strings.Contains(names[i], secret) {
				names[i] = "节点名称已隐藏"
				break
			}
		}
	}
	return names
}

func (s *RemoteService) Nodes(ctx context.Context, actor PersonalSubscriptionActor, id int64) ([]RemoteNode, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,display_name,protocol,format,position FROM remote_subscription_nodes WHERE source_id=? ORDER BY position,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []RemoteNode{}
	ciphertexts := []string{}
	for rows.Next() {
		var node RemoteNode
		var encryptedName string
		if err := rows.Scan(&node.ID, &encryptedName, &node.Protocol, &node.Format, &node.Position); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
		ciphertexts = append(ciphertexts, encryptedName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ciphertexts) > 0 {
		names, err := s.secrets.DecryptMany(ciphertexts)
		if err != nil {
			return nil, ErrRemoteKey
		}
		for i, name := range names {
			nodes[i].Name = safeRemoteName(name)
		}
	}
	return nodes, nil
}

func remoteMetadata(value string) (upload, download, total, expire *int64) {
	for _, item := range strings.Split(value, ";") {
		key, raw, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			upload = &n
		case "download":
			download = &n
		case "total":
			total = &n
		case "expire":
			if n <= 253402300799 {
				expire = &n
			}
		}
	}
	return
}

func remotePreview(parsed remoteParseResult, info, address string) RemotePreview {
	result := RemotePreview{Nodes: make([]RemoteNode, 0, len(parsed.nodes)), Warnings: parsed.warnings, Usable: len(parsed.nodes) > 0}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	names := safeRemoteNodeNames(parsed.nodes, address)
	for i, node := range parsed.nodes {
		result.Nodes = append(result.Nodes, RemoteNode{Name: names[i], Protocol: node.protocol, Format: node.format, Position: i})
	}
	result.Upload, result.Download, result.Total, result.Expire = remoteMetadata(info)
	return result
}

func (s *RemoteService) Test(ctx context.Context, actor PersonalSubscriptionActor, rawURL, userAgent string) (RemotePreview, error) {
	if validatePersonalActor(actor) != nil {
		return RemotePreview{}, ErrRemoteNotFound
	}
	input := RemoteInput{Name: "test", URL: rawURL, UserAgent: userAgent, IntervalSeconds: 300}
	if !validRemoteInput(input, true) {
		return RemotePreview{}, ErrRemoteInvalid
	}
	s.mu.Lock()
	if last := s.previewLast[actor.UserID]; !last.IsZero() && s.now().Sub(last) < remoteMinAttempt {
		s.mu.Unlock()
		return RemotePreview{}, ErrRemoteBusy
	}
	s.previewLast[actor.UserID] = s.now()
	s.mu.Unlock()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		return RemotePreview{}, ErrRemoteBusy
	}
	fetched, err := s.fetcher.fetch(ctx, rawURL, userAgent)
	if err != nil {
		return RemotePreview{}, err
	}
	parsed, err := parseRemoteSubscription(fetched.body)
	if err != nil && len(parsed.warnings) == 0 {
		return RemotePreview{}, err
	}
	return remotePreview(parsed, fetched.subscriptionInfo, rawURL), nil
}

func (s *RemoteService) TestSaved(ctx context.Context, actor PersonalSubscriptionActor, id int64, userAgent string) (RemotePreview, error) {
	if validatePersonalActor(actor) != nil || id <= 0 {
		return RemotePreview{}, ErrRemoteNotFound
	}
	var ciphertext, mode string
	err := s.db.QueryRowContext(ctx, `SELECT url_ciphertext,source_mode FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&ciphertext, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		return RemotePreview{}, ErrRemoteNotFound
	}
	if err != nil {
		return RemotePreview{}, err
	}
	if mode == "manual" {
		return RemotePreview{}, ErrRemoteManual
	}
	address, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return RemotePreview{}, ErrRemoteKey
	}
	return s.Test(ctx, actor, address, userAgent)
}

func (s *RemoteService) Import(ctx context.Context, actor PersonalSubscriptionActor, id int64, body []byte) (RemoteSource, error) {
	if validatePersonalActor(actor) != nil || id <= 0 {
		return RemoteSource{}, ErrRemoteNotFound
	}
	if _, err := s.Get(ctx, actor, id); err != nil {
		return RemoteSource{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteRefreshTimeout)
	defer cancel()
	s.activeMu.Lock()
	if s.active[id] {
		s.activeMu.Unlock()
		return RemoteSource{}, ErrRemoteBusy
	}
	select {
	case s.semaphore <- struct{}{}:
	default:
		s.activeMu.Unlock()
		return RemoteSource{}, ErrRemoteBusy
	}
	s.active[id] = true
	s.activeMu.Unlock()
	defer func() { s.activeMu.Lock(); delete(s.active, id); s.activeMu.Unlock(); <-s.semaphore }()
	var ciphertext string
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT url_ciphertext,revision FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&ciphertext, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteSource{}, ErrRemoteNotFound
	}
	if err != nil {
		return RemoteSource{}, err
	}
	parsed, err := parseRemoteSubscription(body)
	if err != nil || parsed.invalid || len(parsed.nodes) == 0 {
		return RemoteSource{}, ErrRemoteImport
	}
	address, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return RemoteSource{}, ErrRemoteKey
	}
	claimBytes := make([]byte, 16)
	if _, err = rand.Read(claimBytes); err != nil {
		return RemoteSource{}, errors.New("cannot create import claim")
	}
	claim := hex.EncodeToString(claimBytes)
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE remote_subscription_sources SET last_attempt_at=?,last_error='',refresh_claim=?,refresh_lease_until=? WHERE id=? AND owner_user_id=? AND revision=? AND (refresh_lease_until IS NULL OR refresh_lease_until<=?)`, now, claim, now+int64(remoteRefreshLease.Seconds()), id, actor.UserID, revision, now)
	if err != nil {
		return RemoteSource{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		var current int64
		err := s.db.QueryRowContext(ctx, `SELECT revision FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return RemoteSource{}, ErrRemoteNotFound
		}
		if err != nil {
			return RemoteSource{}, err
		}
		if current != revision {
			return RemoteSource{}, ErrRemoteChanged
		}
		return RemoteSource{}, ErrRemoteBusy
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = s.db.ExecContext(cleanup, `UPDATE remote_subscription_sources SET refresh_claim='',refresh_lease_until=NULL WHERE id=? AND owner_user_id=? AND refresh_claim=?`, id, actor.UserID, claim)
	}()
	return s.commitRemoteCache(ctx, actor, id, revision, claim, ciphertext, address, parsed, "")
}

func (s *RemoteService) Refresh(ctx context.Context, actor PersonalSubscriptionActor, id int64) (RemoteSource, error) {
	if validatePersonalActor(actor) != nil || id <= 0 {
		return RemoteSource{}, ErrRemoteNotFound
	}
	value, err := s.Get(ctx, actor, id)
	if err != nil {
		return RemoteSource{}, err
	}
	if value.Mode == "manual" {
		return RemoteSource{}, ErrRemoteManual
	}
	ctx, cancel := context.WithTimeout(ctx, remoteRefreshTimeout)
	defer cancel()
	s.activeMu.Lock()
	if s.active[id] {
		s.activeMu.Unlock()
		return RemoteSource{}, ErrRemoteBusy
	}
	select {
	case s.semaphore <- struct{}{}:
	default:
		s.activeMu.Unlock()
		return RemoteSource{}, ErrRemoteBusy
	}
	s.active[id] = true
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		delete(s.active, id)
		s.activeMu.Unlock()
		<-s.semaphore
	}()

	var ciphertext, userAgent string
	var revision int64
	var lastAttempt sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT url_ciphertext,user_agent,revision,last_attempt_at FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).
		Scan(&ciphertext, &userAgent, &revision, &lastAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteSource{}, ErrRemoteNotFound
	}
	if err != nil {
		return RemoteSource{}, err
	}
	now := s.now().UTC().Unix()
	if lastAttempt.Valid && now-lastAttempt.Int64 < int64(remoteMinAttempt.Seconds()) {
		return RemoteSource{}, ErrRemoteBusy
	}
	claimBytes := make([]byte, 16)
	if _, err := rand.Read(claimBytes); err != nil {
		return RemoteSource{}, errors.New("cannot create refresh claim")
	}
	claim := hex.EncodeToString(claimBytes)
	result, err := s.db.ExecContext(ctx, `UPDATE remote_subscription_sources SET last_attempt_at=?,last_error='',refresh_claim=?,refresh_lease_until=?
		WHERE id=? AND owner_user_id=? AND revision=? AND (last_attempt_at IS NULL OR last_attempt_at<=?)
		AND (refresh_lease_until IS NULL OR refresh_lease_until<=?)`, now, claim, now+int64(remoteRefreshLease.Seconds()), id, actor.UserID, revision,
		now-int64(remoteMinAttempt.Seconds()), now)
	if err != nil {
		return RemoteSource{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		var currentRevision int64
		err := s.db.QueryRowContext(ctx, `SELECT revision FROM remote_subscription_sources WHERE id=? AND owner_user_id=?`, id, actor.UserID).Scan(&currentRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return RemoteSource{}, ErrRemoteNotFound
		}
		if err != nil {
			return RemoteSource{}, err
		}
		if currentRevision != revision {
			return RemoteSource{}, ErrRemoteChanged
		}
		return RemoteSource{}, ErrRemoteBusy
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = s.db.ExecContext(cleanup, `UPDATE remote_subscription_sources SET refresh_claim='',refresh_lease_until=NULL WHERE id=? AND owner_user_id=? AND refresh_claim=?`, id, actor.UserID, claim)
	}()
	address, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return RemoteSource{}, s.refreshFailed(ctx, actor.UserID, id, revision, claim, ErrRemoteKey)
	}
	fetched, err := s.fetcher.fetch(ctx, address, userAgent)
	if err != nil {
		return RemoteSource{}, s.refreshFailed(ctx, actor.UserID, id, revision, claim, err)
	}
	parsed, err := parseRemoteSubscription(fetched.body)
	if err != nil {
		return RemoteSource{}, s.refreshFailedWithWarnings(ctx, actor.UserID, id, revision, claim, err, parsed.warnings)
	}
	if len(parsed.nodes) == 0 {
		return RemoteSource{}, s.refreshFailed(ctx, actor.UserID, id, revision, claim, errRemoteContent)
	}
	return s.commitRemoteCache(ctx, actor, id, revision, claim, ciphertext, address, parsed, fetched.subscriptionInfo)
}

// Commit both downloaded and manually imported nodes with the same lease,
// encryption, stable-identity upsert and atomic cache replacement.
func (s *RemoteService) commitRemoteCache(ctx context.Context, actor PersonalSubscriptionActor, id, revision int64, claim, ciphertext, address string, parsed remoteParseResult, info string) (RemoteSource, error) {
	type encryptedNode struct{ identity, originalName, displayName, protocol, format, payload string }
	values := make([]string, 0, len(parsed.nodes)*3)
	names := safeRemoteNodeNames(parsed.nodes, address)
	for i, node := range parsed.nodes {
		values = append(values, node.originalName, names[i], node.payload)
	}
	ciphertexts, err := s.secrets.EncryptManyExistingChecked(values, ciphertext)
	if err != nil {
		return RemoteSource{}, s.refreshFailed(ctx, actor.UserID, id, revision, claim, ErrRemoteKey)
	}
	encrypted := make([]encryptedNode, 0, len(parsed.nodes))
	for i, node := range parsed.nodes {
		encrypted = append(encrypted, encryptedNode{node.identity, ciphertexts[3*i], ciphertexts[3*i+1], node.protocol, node.format, ciphertexts[3*i+2]})
	}
	warnings, err := json.Marshal(parsed.warnings)
	if err != nil {
		return RemoteSource{}, err
	}
	upload, download, total, expire := remoteMetadata(info)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteSource{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE remote_subscription_sources SET cache_generation=cache_generation+1,last_success_at=?,
		last_error='',warnings_json=?,upload=?,download=?,total=?,expire=?,refresh_claim='',refresh_lease_until=NULL
		WHERE id=? AND owner_user_id=? AND revision=? AND refresh_claim=? AND refresh_lease_until>?`,
		s.now().UTC().Unix(), string(warnings), upload, download, total, expire, id, actor.UserID, revision, claim, s.now().UTC().Unix())
	if err != nil {
		return RemoteSource{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return RemoteSource{}, ErrRemoteChanged
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT cache_generation FROM remote_subscription_sources WHERE id=?`, id).Scan(&generation); err != nil {
		return RemoteSource{}, err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO remote_subscription_nodes(source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,identity) DO UPDATE SET original_name=excluded.original_name,display_name=excluded.display_name,
		protocol=excluded.protocol,format=excluded.format,payload_ciphertext=excluded.payload_ciphertext,position=excluded.position,generation=excluded.generation`)
	if err != nil {
		return RemoteSource{}, err
	}
	for i, node := range encrypted {
		if _, err := statement.ExecContext(ctx, id, node.identity, node.originalName, node.displayName, node.protocol, node.format, node.payload, i, generation); err != nil {
			statement.Close()
			return RemoteSource{}, err
		}
	}
	if err := statement.Close(); err != nil {
		return RemoteSource{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM remote_subscription_nodes WHERE source_id=? AND generation<>?`, id, generation); err != nil {
		return RemoteSource{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteSource{}, err
	}
	value, err := s.Get(ctx, actor, id)
	if err == nil {
		value.Refreshing = false
	}
	return value, err
}

func (s *RemoteService) refreshFailed(ctx context.Context, ownerID, id, revision int64, claim string, cause error) error {
	return s.refreshFailedWithWarnings(ctx, ownerID, id, revision, claim, cause, nil)
}

func (s *RemoteService) refreshFailedWithWarnings(ctx context.Context, ownerID, id, revision int64, claim string, cause error, warnings []string) error {
	message := "远程订阅刷新失败"
	safeCause := errors.New(message)
	switch {
	case errors.Is(cause, ErrRemoteKey):
		safeCause = ErrRemoteKey
	case errors.Is(cause, errRemoteURL):
		safeCause = errRemoteURL
	case errors.Is(cause, errRemoteFetch):
		safeCause = errRemoteFetch
	case errors.Is(cause, errRemoteStatus):
		safeCause = errRemoteStatus
	case errors.Is(cause, errRemoteTooLarge):
		safeCause = errRemoteTooLarge
	case errors.Is(cause, errRemoteContent):
		safeCause = errRemoteContent
	}
	message = safeCause.Error()
	// A cancelled client request must not prevent recording a sanitized status.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var result sql.Result
	var err error
	if warnings != nil {
		encoded, encodeErr := json.Marshal(warnings)
		if encodeErr != nil {
			return encodeErr
		}
		result, err = s.db.ExecContext(writeCtx, `UPDATE remote_subscription_sources SET last_error=?,warnings_json=? WHERE id=? AND owner_user_id=? AND revision=? AND refresh_claim=?`, message, string(encoded), id, ownerID, revision, claim)
	} else {
		result, err = s.db.ExecContext(writeCtx, `UPDATE remote_subscription_sources SET last_error=? WHERE id=? AND owner_user_id=? AND revision=? AND refresh_claim=?`, message, id, ownerID, revision, claim)
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrRemoteChanged
	}
	return safeCause
}

// RemoteFailureMessage returns only fixed, non-sensitive upstream errors.
func RemoteFailureMessage(err error) (string, bool) {
	for _, candidate := range []error{errRemoteURL, errRemoteFetch, errRemoteStatus, errRemoteTooLarge, errRemoteContent} {
		if errors.Is(err, candidate) {
			return candidate.Error(), true
		}
	}
	return "", false
}

// Run refreshes due sources with a bounded worker pool; ordinary public
// subscription requests never perform remote network I/O.
func (s *RemoteService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *RemoteService) refreshDue(ctx context.Context) {
	now := s.now().UTC().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.owner_user_id,u.role FROM remote_subscription_sources s
		JOIN users u ON u.id=s.owner_user_id WHERE s.enabled=1 AND s.source_mode='url' AND u.role IN ('admin','vip')
		AND ((s.refresh_lease_until IS NOT NULL AND s.refresh_lease_until<=?) OR
		(s.refresh_lease_until IS NULL AND (s.last_attempt_at IS NULL OR s.last_attempt_at+s.interval_seconds<=?)))
		ORDER BY COALESCE(s.last_attempt_at,0),s.id LIMIT 64`, now, now)
	if err != nil {
		if ctx.Err() == nil {
			log.Print("remote subscription scheduler query failed")
		}
		return
	}
	type job struct {
		id    int64
		actor PersonalSubscriptionActor
	}
	jobs := []job{}
	for rows.Next() {
		var item job
		if rows.Scan(&item.id, &item.actor.UserID, &item.actor.Role) != nil {
			rows.Close()
			return
		}
		jobs = append(jobs, item)
	}
	if rows.Err() != nil {
		rows.Close()
		return
	}
	rows.Close()
	var workers sync.WaitGroup
	queue := make(chan job)
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range queue {
				if _, err := s.Refresh(ctx, item.actor, item.id); err != nil && !errors.Is(err, ErrRemoteBusy) && ctx.Err() == nil {
					log.Printf("remote subscription refresh failed source_id=%d", item.id)
				}
			}
		}()
	}
	for _, item := range jobs {
		select {
		case queue <- item:
		case <-ctx.Done():
			close(queue)
			workers.Wait()
			return
		}
	}
	close(queue)
	workers.Wait()
}
