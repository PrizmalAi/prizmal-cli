package homeguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiffNamesEachKindOfChange(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.json"), `{"version":1}`)
	write(t, filepath.Join(dir, "token.json"), `{}`)
	write(t, filepath.Join(dir, "backup", "old"), `x`)
	before := Take(dir)

	write(t, filepath.Join(dir, "config.json"), `{"version":1,"default_model":"m"}`)
	if err := os.Remove(filepath.Join(dir, "token.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "device.key"), `k`)

	got := Diff(before, Take(dir))
	want := []string{"added:    device.key", "modified: config.json", "removed:  token.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff = %q, want %q", got, want)
	}
}

// A rewrite with identical bytes still moves the modification time, and the
// guard must report it: the operator's file was touched.
func TestDiffSeesARewriteWithSameBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	write(t, p, `{"version":1}`)
	before := Take(dir)

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if got := Diff(before, Take(dir)); len(got) != 1 || got[0] != "modified: config.json" {
		t.Fatalf("Diff = %q, want config.json modified", got)
	}
}

func TestDiffSeesANewDirectoryAndAbsentDirectoryIsEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".prizmal")
	before := Take(dir)
	if len(before) != 0 {
		t.Fatalf("absent directory fingerprint = %v, want empty", before)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config.json"), `{}`)
	if got := Diff(before, Take(dir)); len(got) != 1 || got[0] != "added:    config.json" {
		t.Fatalf("Diff = %q", got)
	}
}

func TestIdenticalSnapshotsHaveNoDiff(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a"), `a`)
	if got := Diff(Take(dir), Take(dir)); len(got) != 0 {
		t.Fatalf("Diff = %q, want none", got)
	}
}

// Run's sandbox is in effect while tests run: HOME is not the real home.
func TestMainSandboxesHome(t *testing.T) {
	if os.Getenv("HOME") == "" || os.Getenv("HOME") != os.Getenv("USERPROFILE") {
		t.Fatalf("HOME=%q USERPROFILE=%q, want one sandbox directory", os.Getenv("HOME"), os.Getenv("USERPROFILE"))
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), stateDir)); err == nil {
		t.Fatal("sandbox home already holds .prizmal")
	}
}

// Another process refreshing the operator's token changes the real directory
// with bytes the tests never wrote. The package must still pass.
func TestOutsideWriterDoesNotFailTheRun(t *testing.T) {
	realHome := t.TempDir()
	write(t, filepath.Join(realHome, stateDir, "token.json"), `{"v":1}`)

	code := runIn(realHome, func() int {
		write(t, filepath.Join(os.Getenv("HOME"), stateDir, "config.json"), `{"mine":true}`)
		write(t, filepath.Join(realHome, stateDir, "token.json"), `{"v":2}`)
		write(t, filepath.Join(realHome, stateDir, "update-check.json"), `{"t":1}`)
		return 0
	})
	if code != 0 {
		t.Fatalf("runIn = %d, want 0", code)
	}
}

// A test that writes into the real directory through an absolute path leaves
// the same bytes there as in its temp home. The run must fail.
func TestLeakIntoRealHomeFailsTheRun(t *testing.T) {
	realHome := t.TempDir()
	write(t, filepath.Join(realHome, stateDir, "token.json"), `{"v":1}`)

	code := runIn(realHome, func() int {
		body := `{"default_model":"m"}`
		write(t, filepath.Join(os.Getenv("HOME"), stateDir, "config.json"), body)
		write(t, filepath.Join(realHome, stateDir, "config.json"), body)
		return 0
	})
	if code != 1 {
		t.Fatalf("runIn = %d, want 1", code)
	}
}

// A test that moves HOME to the real home and leaves it there is caught even
// when it wrote nothing yet.
func TestHomeLeftOnTheRealHomeFailsTheRun(t *testing.T) {
	realHome := t.TempDir()
	prev := os.Getenv("HOME")
	code := runIn(realHome, func() int {
		_ = os.Setenv("HOME", realHome)
		return 0
	})
	_ = os.Setenv("HOME", prev)
	if code != 1 {
		t.Fatalf("runIn = %d, want 1", code)
	}
}

func TestMain(m *testing.M) { os.Exit(Run(m)) }

// A package added later with tests and no TestMain would run against the real
// home, so every directory with tests must call the guard.
func TestEveryTestPackageRunsUnderTheGuard(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s: %v", root, err)
	}
	hasTests := map[string]bool{}
	guarded := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		hasTests[dir] = true
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), "func TestMain(") &&
			(strings.Contains(string(src), "internaltest.Main(") || strings.Contains(string(src), "homeguard.Run(")) {
			guarded[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir := range hasTests {
		if !guarded[dir] {
			rel, _ := filepath.Rel(root, dir)
			t.Errorf("%s has tests but no TestMain calling internaltest.Main or homeguard.Run", rel)
		}
	}
}
