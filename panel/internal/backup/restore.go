package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

type pendingMarker struct {
	DatabaseSHA256 string `json:"database_sha256"`
}

// RestoreAttempt retains the previous SQLite files until the caller verifies
// that database.Open and migrations succeeded.
type RestoreAttempt struct {
	dataDir    string
	restoreDir string
}

func ApplyPendingRestore(dataDir string) (*RestoreAttempt, error) {
	restoreDir := filepath.Join(dataDir, "restore")
	markerPath := filepath.Join(restoreDir, "pending.json")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		_ = removeIfExists(filepath.Join(restoreDir, "panel.db"))
		_ = removeIfExists(filepath.Join(restoreDir, "pending.json.tmp"))
		_ = os.RemoveAll(filepath.Join(restoreDir, "rollback.tmp"))
		_ = os.RemoveAll(filepath.Join(restoreDir, "rollback"))
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("read restore marker: %w", err)
	}
	attempt := &RestoreAttempt{dataDir: dataDir, restoreDir: restoreDir}
	rollbackDir := filepath.Join(restoreDir, "rollback")
	if _, err := os.Stat(rollbackDir); err == nil {
		if err := attempt.Rollback(); err != nil {
			return nil, fmt.Errorf("%w: recover interrupted restore: %v", ErrRecoveryFailed, err)
		}
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	markerBytes, err := os.ReadFile(markerPath)
	if err != nil {
		return nil, err
	}
	var marker pendingMarker
	staged := filepath.Join(restoreDir, "panel.db")
	if err := json.Unmarshal(markerBytes, &marker); err != nil || len(marker.DatabaseSHA256) != 64 {
		_ = attempt.discardPending()
		return nil, fmt.Errorf("invalid restore marker: %w", ErrInvalidBackup)
	}
	actual, err := fileSHA256(staged)
	if err != nil || actual != marker.DatabaseSHA256 {
		_ = attempt.discardPending()
		return nil, fmt.Errorf("staged database checksum failed: %w", ErrInvalidBackup)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ValidateDatabase(ctx, staged); err != nil {
		_ = attempt.discardPending()
		return nil, fmt.Errorf("staged database invalid: %w", err)
	}
	rollbackTemp := filepath.Join(restoreDir, "rollback.tmp")
	if err := os.RemoveAll(rollbackTemp); err != nil {
		return nil, fmt.Errorf("clear stale rollback workspace: %w", err)
	}
	if err := os.Mkdir(rollbackTemp, 0o700); err != nil {
		_ = attempt.discardPending()
		return nil, fmt.Errorf("prepare rollback: %w", err)
	}
	for _, name := range sqliteFiles {
		source := filepath.Join(dataDir, name)
		if err := copyIfExists(source, filepath.Join(rollbackTemp, name)); err != nil {
			_ = os.RemoveAll(rollbackTemp)
			_ = attempt.discardPending()
			return nil, fmt.Errorf("save rollback: %w", err)
		}
	}
	if err := os.Rename(rollbackTemp, rollbackDir); err != nil {
		_ = os.RemoveAll(rollbackTemp)
		_ = attempt.discardPending()
		return nil, fmt.Errorf("publish rollback: %w", err)
	}
	for _, name := range sqliteFiles {
		if err := removeIfExists(filepath.Join(dataDir, name)); err != nil {
			return nil, attempt.rollbackOnError(err)
		}
	}
	if err := os.Rename(staged, filepath.Join(dataDir, "panel.db")); err != nil {
		return nil, attempt.rollbackOnError(err)
	}
	return attempt, nil
}

var sqliteFiles = []string{"panel.db", "panel.db-wal", "panel.db-shm"}

func (a *RestoreAttempt) Commit() error {
	if err := os.Remove(filepath.Join(a.restoreDir, "pending.json")); err != nil {
		return fmt.Errorf("clear restore marker: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(a.restoreDir, "rollback")); err != nil {
		// The marker is already gone, so this restore is committed.
		// Retaining an old rollback copy is safer than reapplying it.
		log.Printf("restore committed; rollback cleanup failed: %v", err)
		return nil
	}
	return nil
}

func (a *RestoreAttempt) Rollback() error {
	rollbackDir := filepath.Join(a.restoreDir, "rollback")
	if _, err := os.Stat(rollbackDir); err != nil {
		return fmt.Errorf("rollback unavailable: %w", err)
	}
	for _, name := range sqliteFiles {
		if err := removeIfExists(filepath.Join(a.dataDir, name)); err != nil {
			return err
		}
	}
	for _, name := range sqliteFiles {
		if err := copyIfExists(filepath.Join(rollbackDir, name), filepath.Join(a.dataDir, name)); err != nil {
			return err
		}
	}
	if err := a.discardPending(); err != nil {
		return err
	}
	return os.RemoveAll(rollbackDir)
}

func (a *RestoreAttempt) rollbackOnError(cause error) error {
	if err := a.Rollback(); err != nil {
		return fmt.Errorf("%w: restore failed: %v; rollback failed: %v", ErrRecoveryFailed, cause, err)
	}
	return fmt.Errorf("restore failed and original database recovered: %w", cause)
}

func (a *RestoreAttempt) discardPending() error {
	for _, name := range []string{"pending.json", "panel.db"} {
		if err := removeIfExists(filepath.Join(a.restoreDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func copyIfExists(source, destination string) error {
	input, err := os.Open(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrInvalidBackup
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
