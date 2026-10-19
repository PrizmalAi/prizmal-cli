package internaltest

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeKeyAtRest is an obvious non-credential used to prove where a key would
// land. It must never look like a real provider key.
const FakeKeyAtRest = "not-a-real-key-credential-at-rest-probe"

// FilesContainingKey walks root and returns every file whose bytes contain the
// probe key. Directories that do not exist are simply empty.
func FilesContainingKey(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries cannot hold a key we care about
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), FakeKeyAtRest) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hits
}

// SandboxedJSON reads a config file relative to a sandboxed home and parses
// it, the one reader every credential-at-rest test uses.
func SandboxedJSON(t *testing.T, home, relPath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", relPath, err)
	}
	return cfg
}

// SandboxedSeed writes body to a file relative to a sandboxed home, creating
// the parent directories, the way a user's own harness install would look.
func SandboxedSeed(t *testing.T, home, relPath, body string) {
	t.Helper()
	path := filepath.Join(home, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
}

// AssertNoKeyUnder fails the test when the probe key sits in any file under
// root. what names the step that ran, for the message.
func AssertNoKeyUnder(t *testing.T, root, what string) {
	t.Helper()
	if hits := FilesContainingKey(t, root); len(hits) > 0 {
		t.Fatalf("%s left the provider key on disk in: %v", what, hits)
	}
}

// AssertOverwriteLeavesNoKeyInBackup is the trap in the credential-at-rest
// fix: the shared WriteWithBackup helper copies the file it is about to
// overwrite into ~/.prizmal/backup, so an overwrite of a key-bearing
// predecessor must not deposit that key one directory over. target is the
// seeded file, and overwrite runs the launcher path that rewrites it.
func AssertOverwriteLeavesNoKeyInBackup(t *testing.T, home, target string, overwrite func() error) {
	t.Helper()
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read seeded %s: %v", target, err)
	}
	if !strings.Contains(string(before), FakeKeyAtRest) {
		t.Fatalf("precondition failed: seeded %s carries no key", target)
	}
	if err := overwrite(); err != nil {
		t.Fatalf("overwrite %s: %v", target, err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read overwritten %s: %v", target, err)
	}
	if string(after) == string(before) {
		t.Fatalf("precondition failed: %s was not overwritten, so no backup was taken", target)
	}
	backupDir := filepath.Join(home, ".prizmal", "backup")
	if hits := FilesContainingKey(t, backupDir); len(hits) > 0 {
		t.Fatalf("the launcher retained the provider key under ~/.prizmal/backup: %v", hits)
	}
}

// AssertRestoreRemovesKey seeds nothing itself: the caller has put a
// key-bearing config under home, and restore must take every copy of the key
// off the disk without copying it into the backup directory.
func AssertRestoreRemovesKey(t *testing.T, home string, restore func() error) {
	t.Helper()
	if hits := FilesContainingKey(t, home); len(hits) == 0 {
		t.Fatal("precondition failed: the seed wrote no key, nothing to restore")
	}
	if err := restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if hits := FilesContainingKey(t, home); len(hits) > 0 {
		t.Fatalf("Restore left the provider key on disk in: %v", hits)
	}
}
