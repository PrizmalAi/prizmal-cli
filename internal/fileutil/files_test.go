package fileutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// fakeKey is an obvious non-credential standing in for the provider key.
const fakeKey = "not-a-real-key-fileutil-backup-probe"

// sandboxHome points HOME at a temp dir so BackupDir never resolves to the
// operator's real ~/.prizmal/backup.
func sandboxHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	return d
}

// backupNames lists the retained backups of name under dir.
func backupNames(t *testing.T, dir, name string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var got []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), name+".") {
			got = append(got, e.Name())
		}
	}
	return got
}

// TestWriteWithBackupKeepsCopyOfKeylessFile is the half of the backup feature
// that must survive. A config with no credential in it is exactly what the
// retention policy is for: the user gets their settings back.
func TestWriteWithBackupKeepsCopyOfKeylessFile(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	target := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := WriteWithBackup(target, []byte(`{"theme":"light"}`), "probe"); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}

	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	names := backupNames(t, backupDir, "settings.json")
	if len(names) != 1 {
		t.Fatalf("retained backups = %v, want exactly one copy of the keyless original", names)
	}
	data, err := os.ReadFile(filepath.Join(backupDir, names[0]))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("backup content = %q, want the original settings", data)
	}
}

// TestWriteWithBackupKeepsNoCopyOfKeyBearingFile is the defect: overwriting a
// config that carries the live key used to leave a copy of it behind.
func TestWriteWithBackupKeepsNoCopyOfKeyBearingFile(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	target := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(target, []byte(`{"apiKey":"`+fakeKey+`"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := WriteWithBackup(target, []byte(`{"apiKey":"`+fakeKey+`","model":"next"}`), "probe"); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}

	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	if names := backupNames(t, backupDir, "models.json"); len(names) != 0 {
		t.Fatalf("retained backups = %v, want none of a key-bearing file", names)
	}
	// The write itself must still have happened.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(data), `"model":"next"`) {
		t.Fatalf("target = %q, want the new content written", data)
	}
}

// TestWriteWithBackupPurgesPreexistingKeyBearingBackups covers the installs
// that already accumulated copies under an older build: the next write to the
// same file clears them, and leaves innocent backups alone.
func TestWriteWithBackupPurgesPreexistingKeyBearingBackups(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tainted := filepath.Join(backupDir, "models.json.1700000000")
	if err := os.WriteFile(tainted, []byte(`{"apiKey":"`+fakeKey+`"}`), 0o600); err != nil {
		t.Fatalf("seed tainted backup: %v", err)
	}
	innocent := filepath.Join(backupDir, "models.json.1600000000")
	if err := os.WriteFile(innocent, []byte(`{"providers":{}}`), 0o600); err != nil {
		t.Fatalf("seed innocent backup: %v", err)
	}

	target := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(target, []byte(`{"providers":{}}`), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := WriteWithBackup(target, []byte(`{"providers":{"a":1}}`), "probe"); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}

	if _, err := os.Stat(tainted); !os.IsNotExist(err) {
		t.Fatalf("stat tainted backup = %v, want it removed", err)
	}
	if _, err := os.Stat(innocent); err != nil {
		t.Fatalf("innocent backup was removed: %v", err)
	}
}

// TestWriteWithBackupUnsetKeyBacksUpNormally guards the empty-key case: an
// unset credential must not make every byte on disk look like a match.
func TestWriteWithBackupUnsetKeyBacksUpNormally(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, "")

	target := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(target, []byte("model = \"a\"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteWithBackup(target, []byte("model = \"b\"\n"), "probe"); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}

	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	if names := backupNames(t, backupDir, "config.toml"); len(names) != 1 {
		t.Fatalf("retained backups = %v, want the ordinary single copy", names)
	}
}

// TestWriteWithBackupKeepsBothCopiesWrittenWithinOneSecond: a backup is
// named by the second it was taken, so two overwrites inside one second used
// to leave one copy, the newer, and the pre-launch original was gone. Restore
// reads that original back, so it has to survive a quick second persist.
func TestWriteWithBackupKeepsBothCopiesWrittenWithinOneSecond(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte(`{"n":0}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, body := range []string{`{"n":1}`, `{"n":2}`} {
		if err := WriteWithBackup(target, []byte(body), "probe"); err != nil {
			t.Fatalf("WriteWithBackup(%s): %v", body, err)
		}
	}

	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	if names := backupNames(t, backupDir, "settings.json"); len(names) != 2 {
		t.Fatalf("retained backups = %v, want both copies", names)
	}
	var got []string
	for _, path := range Backups("probe", "settings.json") {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		got = append(got, string(data))
	}
	if want := []string{`{"n":1}`, `{"n":0}`}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Backups newest first = %v, want %v", got, want)
	}
}

// TestBackupsListsNewestFirst pins the order Restore relies on and the
// filter: only <name>.<unix-seconds> entries of the named file count.
func TestBackupsListsNewestFirst(t *testing.T) {
	home := sandboxHome(t)
	backupDir := filepath.Join(home, ".prizmal", "backup", "probe")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"settings.json.100", "settings.json.300", "settings.json.200", "settings.json.bogus", "other.json.400"} {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("{}"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	var got []string
	for _, path := range Backups("probe", "settings.json") {
		got = append(got, filepath.Base(path))
	}
	want := []string{"settings.json.300", "settings.json.200", "settings.json.100"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Backups = %v, want %v", got, want)
	}
	if got := Backups("probe", "never-written.json"); len(got) != 0 {
		t.Fatalf("Backups of a file never backed up = %v, want none", got)
	}
}
