package device

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeKeyAndToken puts a device key and a cached token under dir.
func writeKeyAndToken(t *testing.T, dir string) *Key {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := key.WriteKeyFile(dir); err != nil {
		t.Fatal(err)
	}
	ct := &CachedToken{Token: "tok", Expiry: time.Now().Add(time.Minute)}
	if err := ct.saveTo(dir); err != nil {
		t.Fatal(err)
	}
	return key
}

// Backing up moves the key to a file next to it, with a timestamp in the name
// and the same private permissions, and leaves no key at the old path.
func TestRetireKeyMovesItToATimestampedBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".prizmal")
	key := writeKeyAndToken(t, dir)
	now := time.Date(2026, 11, 11, 15, 4, 5, 0, time.UTC)

	backup, err := retireKeyIn(dir, true, now)
	if err != nil {
		t.Fatalf("retireKeyIn: %v", err)
	}
	if filepath.Dir(backup) != dir || !strings.Contains(filepath.Base(backup), "20261111T150405Z") {
		t.Fatalf("backup path %q is not a timestamped file next to the key", backup)
	}
	if _, err := os.Stat(keyPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the key is still at its old path: %v", err)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits to check.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(backup)
	if !bytes.Equal(data, key.Priv.Seed()) {
		t.Fatal("the backup does not hold the original key")
	}
	if _, err := os.Stat(tokenPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the revoked device's cached token survived: %v", err)
	}
}

// Declining the backup removes the key and leaves no file behind.
func TestRetireKeyWithoutBackupRemovesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".prizmal")
	writeKeyAndToken(t, dir)

	backup, err := retireKeyIn(dir, false, time.Now())
	if err != nil {
		t.Fatalf("retireKeyIn: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup path = %q, want none", backup)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("the directory still holds %d files", len(entries))
	}
}

// Two retirements in the same second must not overwrite the first backup.
func TestRetireKeyNeverOverwritesAnEarlierBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".prizmal")
	now := time.Date(2026, 11, 11, 15, 4, 5, 0, time.UTC)
	writeKeyAndToken(t, dir)
	first, err := retireKeyIn(dir, true, now)
	if err != nil {
		t.Fatal(err)
	}
	writeKeyAndToken(t, dir)
	second, err := retireKeyIn(dir, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both backups went to %q", first)
	}
}

// A revocation that lands while a login waits for approval ends it with the
// next step, not a bare error.
func TestLoginErrorForRevokedDeviceSaysRunLogin(t *testing.T) {
	err := loginError(ErrDeviceRevoked)
	if !strings.Contains(err.Error(), "revoked") || !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("loginError(ErrDeviceRevoked) = %q, want the revocation and a login hint", err)
	}
}
