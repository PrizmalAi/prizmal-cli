// Package fileutil provides small shared helpers for reading JSON files
// and writing config files with backup-on-overwrite semantics.
//
// The backup half of that deal is deliberately credential-aware. Several
// harness configs carry the live provider key in plaintext, and a retention
// policy that quietly kept five historical copies of them was a second
// credential at rest that no remedy reached. This package therefore knows
// what the current key is, so it can decline to keep a copy of it.
package fileutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Keep a bounded number of backups per file so config backups do not grow
// without limit. We keep the 5 most recent backups and do not pin the oldest.
const maxBackupsPerFile = 5

// ReadJSON reads a JSON object file into a generic map.
func ReadJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// BackupDir returns the shared backup root used before overwriting files.
func BackupDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".prizmal", "backup")
	}
	return filepath.Join(os.TempDir(), "prizmal-backup")
}

// backupDirFor returns the directory backups of an integration's files go in.
func backupDirFor(integration string) string {
	if integration == "" {
		return BackupDir()
	}
	return filepath.Join(BackupDir(), integration)
}

// carriesCredential reports whether data contains the provider key this
// process is configured with. It is an exact match against the one secret we
// actually hold rather than a guess at what a key looks like: a heuristic
// would both miss keys and refuse to back up innocent files. An unset key
// matches nothing.
func carriesCredential(data []byte) bool {
	key := strings.TrimSpace(envconfig.APIKey())
	if key == "" {
		return false
	}
	return bytes.Contains(data, []byte(key))
}

// dropKeyBearingBackups removes retained backups of name in dir that carry the
// live credential. It runs on every write, not only the ones that skip a
// backup, so an install that accumulated key-bearing copies under an older
// build heals itself the next time prizmal touches the same file.
func dropKeyBearingBackups(dir, name string) {
	for _, backup := range listBackups(dir, name) {
		path := filepath.Join(dir, backup.name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if carriesCredential(data) {
			_ = os.Remove(path)
		}
	}
}

func writeBackupCopy(srcPath string, integration string) (string, error) {
	dir := backupDirFor(integration)
	name := filepath.Base(srcPath)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// Backups are named by the second, so a second persist inside the same
	// second would overwrite the first copy, the pre-launch original that
	// Restore reads back. Take the next free second instead.
	stamp := time.Now().Unix()
	backupPath := filepath.Join(dir, fmt.Sprintf("%s.%d", name, stamp))
	for {
		if _, err := os.Lstat(backupPath); os.IsNotExist(err) {
			break
		}
		stamp++
		backupPath = filepath.Join(dir, fmt.Sprintf("%s.%d", name, stamp))
	}
	if err := copyFile(srcPath, backupPath); err != nil {
		return "", err
	}
	pruneOldBackups(dir, name, maxBackupsPerFile)
	return backupPath, nil
}

// WriteWithBackup writes data to path via temp file + rename, backing up any
// existing file first. Callers may optionally pass one integration name to
// store backups under BackupDir()/.../<integration>/.
func WriteWithBackup(path string, data []byte, integration ...string) error {
	backupIntegration := ""
	if len(integration) > 0 {
		backupIntegration = integration[0]
	}

	var backupPath string
	// backup must be created before any writes to the target file
	if existingContent, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existingContent, data) {
			return nil
		}
		// A file holding the live key gets overwritten with no copy kept.
		// Backups exist so a user can recover settings they did not mean to
		// lose; that is worth keeping for files with no key in them, and
		// worth nothing next to leaving the credential somewhere --restore
		// does not reach.
		if !carriesCredential(existingContent) {
			backupPath, err = writeBackupCopy(path, backupIntegration)
			if err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing file: %w", err)
	}

	dropKeyBearingBackups(backupDirFor(backupIntegration), filepath.Base(path))

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp failed: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write failed: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close failed: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		if backupPath != "" {
			_ = copyFile(backupPath, path)
		}
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}

// Backups lists the retained backups of an integration's file, newest first,
// as absolute paths. Restore walks it to find the copy taken before the
// first launch repointed the file.
func Backups(integration, name string) []string {
	dir := backupDirFor(integration)
	var paths []string
	for _, backup := range listBackups(dir, name) {
		paths = append(paths, filepath.Join(dir, backup.name))
	}
	return paths
}

type backupEntry struct {
	name      string
	timestamp int64
}

// listBackups returns the <name>.<unix-seconds> entries of name in dir,
// newest first.
func listBackups(dir, name string) []backupEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	prefix := name + "."
	backups := make([]backupEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		timestamp, err := strconv.ParseInt(strings.TrimPrefix(entry.Name(), prefix), 10, 64)
		if err != nil {
			continue
		}
		backups = append(backups, backupEntry{name: entry.Name(), timestamp: timestamp})
	}

	sort.Slice(backups, func(i, j int) bool {
		if backups[i].timestamp != backups[j].timestamp {
			return backups[i].timestamp > backups[j].timestamp
		}
		return backups[i].name > backups[j].name
	})
	return backups
}

func pruneOldBackups(dir, name string, keep int) {
	if keep < 1 {
		return
	}
	backups := listBackups(dir, name)
	if len(backups) <= keep {
		return
	}
	for _, backup := range backups[keep:] {
		_ = os.Remove(filepath.Join(dir, backup.name))
	}
}
