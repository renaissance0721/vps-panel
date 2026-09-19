package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/version"
	_ "modernc.org/sqlite"
)

const (
	MaxArchiveSize = 512 << 20
	maxExtractSize = 1 << 30
	formatName     = "vps-panel-backup"
	formatVersion  = 1
)

var (
	ErrInvalidBackup  = errors.New("invalid backup")
	ErrPendingRestore = errors.New("a restore is already pending")
	ErrRecoveryFailed = errors.New("restore rollback failed")
)

type DomainMismatchError struct{ BackupDomain string }

func (e DomainMismatchError) Error() string {
	return fmt.Sprintf("备份来自 %s，请先将当前 Panel 域名切换为相同地址后再导入。", e.BackupDomain)
}

type manifest struct {
	Format         string `json:"format"`
	FormatVersion  int    `json:"format_version"`
	CreatedAt      string `json:"created_at"`
	PanelVersion   string `json:"panel_version"`
	Database       string `json:"database"`
	PanelDomain    string `json:"panel_domain"`
	DatabaseSHA256 string `json:"database_sha256"`
}

// CreateArchive takes a live SQLite snapshot and returns a private ZIP path and its cleanup function.
func CreateArchive(ctx context.Context, db *sql.DB, dataDir, panelVersion, panelDomain, environmentFile, caddyFile string) (string, func(), error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return "", nil, fmt.Errorf("prepare backup directory: %w", err)
	}
	tempDir, err := os.MkdirTemp(dataDir, "backup-export-*")
	if err != nil {
		return "", nil, fmt.Errorf("create backup workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	fail := func(err error) (string, func(), error) { cleanup(); return "", nil, err }
	snapshot := filepath.Join(tempDir, "panel.db")
	file, err := os.OpenFile(snapshot, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(fmt.Errorf("create snapshot file: %w", err))
	}
	if err := file.Close(); err != nil {
		return fail(fmt.Errorf("close snapshot file: %w", err))
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, snapshot); err != nil {
		return fail(fmt.Errorf("snapshot database: %w", err))
	}
	if err := ValidateDatabase(ctx, snapshot); err != nil {
		return fail(fmt.Errorf("validate snapshot: %w", err))
	}
	stat, err := os.Stat(snapshot)
	if err != nil || stat.Size() > maxExtractSize {
		return fail(fmt.Errorf("snapshot exceeds backup size limit: %w", ErrInvalidBackup))
	}
	dbHash, err := fileSHA256(snapshot)
	if err != nil {
		return fail(err)
	}
	meta, err := json.MarshalIndent(manifest{
		Format: formatName, FormatVersion: formatVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		PanelVersion: panelVersion, Database: "data/panel.db", PanelDomain: panelDomain, DatabaseSHA256: dbHash,
	}, "", "  ")
	if err != nil {
		return fail(fmt.Errorf("encode backup manifest: %w", err))
	}
	contents := map[string][]byte{"manifest.json": append(meta, '\n')}
	for name, source := range map[string]string{
		"deployment/environment":     environmentFile,
		"deployment/vps-panel.caddy": caddyFile,
	} {
		if source == "" {
			continue
		}
		info, err := os.Stat(source)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 1<<20 {
			return fail(fmt.Errorf("deployment file exceeds backup size limit: %w", ErrInvalidBackup))
		}
		data, err := os.ReadFile(source)
		if errors.Is(err, os.ErrPermission) {
			continue
		}
		if err != nil {
			return fail(fmt.Errorf("read deployment file: %w", err))
		}
		contents[name] = data
	}
	checksums := map[string]string{"data/panel.db": dbHash}
	for name, data := range contents {
		digest := sha256.Sum256(data)
		checksums[name] = hex.EncodeToString(digest[:])
	}
	names := make([]string, 0, len(checksums))
	for name := range checksums {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sums, "%s  %s\n", checksums[name], name)
	}
	contents["SHA256SUMS"] = []byte(sums.String())
	archivePath := filepath.Join(tempDir, "backup.zip")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(fmt.Errorf("create backup ZIP: %w", err))
	}
	writer := zip.NewWriter(archive)
	for _, name := range []string{"manifest.json", "data/panel.db", "deployment/environment", "deployment/vps-panel.caddy", "SHA256SUMS"} {
		data, present := contents[name]
		if name != "data/panel.db" && !present {
			continue
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			writer.Close()
			archive.Close()
			return fail(fmt.Errorf("add backup entry: %w", err))
		}
		if name == "data/panel.db" {
			source, err := os.Open(snapshot)
			if err != nil {
				writer.Close()
				archive.Close()
				return fail(err)
			}
			_, err = io.Copy(entry, source)
			source.Close()
			if err != nil {
				writer.Close()
				archive.Close()
				return fail(fmt.Errorf("write snapshot to ZIP: %w", err))
			}
		} else if _, err := entry.Write(data); err != nil {
			writer.Close()
			archive.Close()
			return fail(fmt.Errorf("write backup entry: %w", err))
		}
	}
	if err := writer.Close(); err != nil {
		archive.Close()
		return fail(fmt.Errorf("finish backup ZIP: %w", err))
	}
	if err := archive.Close(); err != nil {
		return fail(fmt.Errorf("close backup ZIP: %w", err))
	}
	archiveStat, err := os.Stat(archivePath)
	if err != nil || archiveStat.Size() > MaxArchiveSize {
		return fail(fmt.Errorf("backup ZIP exceeds size limit: %w", ErrInvalidBackup))
	}
	return archivePath, cleanup, nil
}

// StageImport validates the entire ZIP before publishing a pending restore marker.
func StageImport(ctx context.Context, archivePath, dataDir, currentVersion, currentDomain string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("prepare data directory: %w", err)
	}
	tempDir, err := os.MkdirTemp(dataDir, "backup-import-*")
	if err != nil {
		return fmt.Errorf("create import workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)
	stat, err := os.Stat(archivePath)
	if err != nil || stat.Size() > MaxArchiveSize {
		return fmt.Errorf("ZIP 超过大小限制: %w", ErrInvalidBackup)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("ZIP 无法解析: %w", ErrInvalidBackup)
	}
	defer reader.Close()
	allowed := map[string]int64{"manifest.json": 64 << 10, "SHA256SUMS": 4 << 10, "data/panel.db": maxExtractSize,
		"deployment/environment": 1 << 20, "deployment/vps-panel.caddy": 1 << 20}
	entries := make(map[string]*zip.File)
	var declared uint64
	for _, entry := range reader.File {
		limit, ok := allowed[entry.Name]
		if !ok || entry.Name != path.Clean(entry.Name) || strings.ContainsAny(entry.Name, `\:`) ||
			strings.HasPrefix(entry.Name, "/") || !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || entries[entry.Name] != nil {
			return fmt.Errorf("ZIP 包含不允许的路径或文件: %w", ErrInvalidBackup)
		}
		if entry.UncompressedSize64 > uint64(limit) {
			return fmt.Errorf("ZIP 文件超过大小限制: %w", ErrInvalidBackup)
		}
		if entry.UncompressedSize64 > maxExtractSize-declared {
			return fmt.Errorf("ZIP 解压总量超过限制: %w", ErrInvalidBackup)
		}
		declared += entry.UncompressedSize64
		entries[entry.Name] = entry
	}
	if entries["manifest.json"] == nil || entries["SHA256SUMS"] == nil || entries["data/panel.db"] == nil {
		return fmt.Errorf("ZIP 缺少必需文件: %w", ErrInvalidBackup)
	}
	actual := make(map[string]string)
	var meta manifest
	for _, name := range []string{"manifest.json", "deployment/environment", "deployment/vps-panel.caddy", "data/panel.db"} {
		entry := entries[name]
		if entry == nil {
			continue
		}
		var output io.Writer
		var outputFile *os.File
		var buffer bytes.Buffer
		if name == "data/panel.db" {
			file, err := os.OpenFile(filepath.Join(tempDir, "panel.db"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("stage imported database: %w", err)
			}
			outputFile = file
			output = file
		} else {
			output = &buffer
		}
		hash := sha256.New()
		part, err := entry.Open()
		if err != nil {
			if outputFile != nil {
				_ = outputFile.Close()
			}
			return fmt.Errorf("read ZIP entry: %w", ErrInvalidBackup)
		}
		count, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(part, allowed[name]+1))
		closeErr := part.Close()
		if outputFile != nil {
			if err := outputFile.Sync(); err != nil {
				_ = outputFile.Close()
				return fmt.Errorf("sync staged database: %w", err)
			}
			if err := outputFile.Close(); err != nil {
				return fmt.Errorf("close staged database: %w", err)
			}
		}
		if copyErr != nil || closeErr != nil || count > allowed[name] {
			return fmt.Errorf("ZIP entry invalid or oversized: %w", ErrInvalidBackup)
		}
		actual[name] = hex.EncodeToString(hash.Sum(nil))
		if name == "manifest.json" {
			decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&meta); err != nil {
				return fmt.Errorf("manifest 无效: %w", ErrInvalidBackup)
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				return fmt.Errorf("manifest 包含多余内容: %w", ErrInvalidBackup)
			}
		}
	}
	sumsData, err := readEntry(entries["SHA256SUMS"], allowed["SHA256SUMS"])
	if err != nil {
		return err
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(sumsData)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || sums[parts[1]] != "" {
			return fmt.Errorf("SHA256SUMS 无效: %w", ErrInvalidBackup)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return fmt.Errorf("SHA256SUMS 无效: %w", ErrInvalidBackup)
		}
		sums[parts[1]] = parts[0]
	}
	if len(sums) != len(actual) {
		return fmt.Errorf("SHA256SUMS 文件列表不匹配: %w", ErrInvalidBackup)
	}
	for name, hash := range actual {
		if sums[name] != hash {
			return fmt.Errorf("SHA256SUMS 校验失败: %w", ErrInvalidBackup)
		}
	}
	if meta.Format != formatName || meta.FormatVersion != formatVersion || meta.Database != "data/panel.db" || meta.DatabaseSHA256 != actual["data/panel.db"] || meta.PanelDomain == "" || meta.PanelVersion == "" {
		return fmt.Errorf("备份格式或版本不受支持: %w", ErrInvalidBackup)
	}
	if comparison, ok := version.Compare(meta.PanelVersion, currentVersion); ok {
		if comparison > 0 {
			return fmt.Errorf("备份由更新版本 Panel 创建，请先升级当前 Panel: %w", ErrInvalidBackup)
		}
	} else if meta.PanelVersion != currentVersion {
		return fmt.Errorf("无法确认备份版本与当前 Panel 兼容: %w", ErrInvalidBackup)
	}
	if meta.PanelDomain != currentDomain {
		return DomainMismatchError{BackupDomain: meta.PanelDomain}
	}
	staged := filepath.Join(tempDir, "panel.db")
	if err := ValidateDatabase(ctx, staged); err != nil {
		return fmt.Errorf("备份数据库校验失败: %w", ErrInvalidBackup)
	}
	restoreDir := filepath.Join(dataDir, "restore")
	if err := os.MkdirAll(restoreDir, 0o700); err != nil {
		return fmt.Errorf("prepare restore directory: %w", err)
	}
	for _, name := range []string{"pending.json", "panel.db"} {
		if _, err := os.Stat(filepath.Join(restoreDir, name)); err == nil {
			return ErrPendingRestore
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect restore state: %w", err)
		}
	}
	if err := os.Rename(staged, filepath.Join(restoreDir, "panel.db")); err != nil {
		return fmt.Errorf("stage restore database: %w", err)
	}
	pendingData, _ := json.Marshal(pendingMarker{DatabaseSHA256: actual["data/panel.db"]})
	marker, err := os.OpenFile(filepath.Join(restoreDir, "pending.json.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.Remove(filepath.Join(restoreDir, "panel.db"))
		return fmt.Errorf("stage restore marker: %w", err)
	}
	_, writeErr := marker.Write(pendingData)
	syncErr := marker.Sync()
	closeErr := marker.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(filepath.Join(restoreDir, "panel.db"))
		os.Remove(marker.Name())
		return errors.New("write restore marker failed")
	}
	if err := os.Rename(marker.Name(), filepath.Join(restoreDir, "pending.json")); err != nil {
		os.Remove(filepath.Join(restoreDir, "panel.db"))
		os.Remove(marker.Name())
		return fmt.Errorf("publish restore marker: %w", err)
	}
	return nil
}

func readEntry(entry *zip.File, limit int64) ([]byte, error) {
	part, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("read ZIP entry: %w", ErrInvalidBackup)
	}
	defer part.Close()
	data, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("ZIP entry invalid or oversized: %w", ErrInvalidBackup)
	}
	return data, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ValidateDatabase reads a database without modifying its schema or rows.
func ValidateDatabase(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	header := make([]byte, 16)
	_, err = io.ReadFull(file, header)
	file.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return fmt.Errorf("invalid SQLite header: %w", ErrInvalidBackup)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		return err
	}
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		return fmt.Errorf("SQLite integrity_check failed: %w", ErrInvalidBackup)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	if rows.Next() {
		rows.Close()
		return fmt.Errorf("SQLite foreign_key_check failed: %w", ErrInvalidBackup)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for table, columns := range map[string][]string{
		"users":             {"id", "username", "password_hash", "role"},
		"sessions":          {"id", "user_id", "token_hash"},
		"admin_invitations": {"id", "token_hash", "created_by"},
		"servers":           {"id", "name", "status", "desired_state_version"},
		"agents":            {"id", "server_id", "token_hash"},
		"proxies":           {"id", "server_id", "config_json"},
		"clients":           {"id", "proxy_id", "credential_json"},
		"relays":            {"id", "server_id", "target_type"},
	} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			return fmt.Errorf("backup core schema is incomplete: %w", ErrInvalidBackup)
		}
		for _, column := range columns {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil || count != 1 {
				return fmt.Errorf("backup core schema is incomplete: %w", ErrInvalidBackup)
			}
		}
	}
	return nil
}
