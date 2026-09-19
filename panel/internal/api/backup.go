package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/backup"
)

func (s *server) exportBackup(w http.ResponseWriter, r *http.Request, _ auth.User) {
	if s.backup.DataDir == "" || s.backup.Domain == "" {
		writeError(w, http.StatusServiceUnavailable, "请先配置 PANEL_DOMAIN 和 PANEL_DATA_DIR")
		return
	}
	archivePath, cleanup, err := backup.CreateArchive(r.Context(), s.db, s.backup.DataDir,
		s.panelVersion, s.backup.Domain, s.backup.EnvironmentFile, s.backup.CaddyFile)
	if err != nil {
		writeInternalError(w)
		return
	}
	defer cleanup()
	file, err := os.Open(archivePath)
	if err != nil {
		writeInternalError(w)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeInternalError(w)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vps-panel-backup-%s.zip"`, time.Now().Format("20060102-150405")))
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, file)
}

func (s *server) importBackup(w http.ResponseWriter, r *http.Request, _ auth.User) {
	if s.backup.DataDir == "" || s.backup.Domain == "" || s.backup.RestoreRequested == nil {
		writeError(w, http.StatusServiceUnavailable, "请先配置 PANEL_DOMAIN 和 PANEL_DATA_DIR")
		return
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeError(w, http.StatusBadRequest, "请上传 ZIP 备份文件")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxArchiveSize+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "上传格式无效")
		return
	}
	tempDir, err := os.MkdirTemp(s.backup.DataDir, "backup-upload-*")
	if err != nil {
		writeInternalError(w)
		return
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, "upload.zip")
	haveBackup, haveConfirmation := false, false
	confirmation := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "上传内容无效或超过大小限制")
			return
		}
		switch part.FormName() {
		case "backup":
			if haveBackup || part.FileName() == "" {
				writeError(w, http.StatusBadRequest, "备份文件无效")
				return
			}
			haveBackup = true
			file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				writeInternalError(w)
				return
			}
			count, copyErr := io.Copy(file, io.LimitReader(part, backup.MaxArchiveSize+1))
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil || count > backup.MaxArchiveSize {
				writeError(w, http.StatusBadRequest, "ZIP 超过大小限制或上传失败")
				return
			}
		case "confirmation":
			if haveConfirmation {
				writeError(w, http.StatusBadRequest, "重复的确认字段")
				return
			}
			haveConfirmation = true
			value, err := io.ReadAll(io.LimitReader(part, 64))
			if err != nil {
				writeError(w, http.StatusBadRequest, "确认字段无效")
				return
			}
			confirmation = string(value)
		default:
			writeError(w, http.StatusBadRequest, "上传字段无效")
			return
		}
		_ = part.Close()
	}
	if !haveBackup || !haveConfirmation || confirmation != "RESTORE" {
		writeError(w, http.StatusBadRequest, "请输入 RESTORE 确认完整覆盖恢复")
		return
	}
	if err := backup.StageImport(r.Context(), archivePath, s.backup.DataDir, s.panelVersion, s.backup.Domain); err != nil {
		var mismatch backup.DomainMismatchError
		switch {
		case errors.As(err, &mismatch):
			writeError(w, http.StatusBadRequest, mismatch.Error())
		case errors.Is(err, backup.ErrPendingRestore):
			writeError(w, http.StatusConflict, "已有待执行的恢复任务")
		case errors.Is(err, backup.ErrInvalidBackup):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeInternalError(w)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restore_pending"})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	select {
	case s.backup.RestoreRequested <- struct{}{}:
	default:
	}
}
