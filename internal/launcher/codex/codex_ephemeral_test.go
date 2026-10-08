package codex

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// runFakeCodex launches the runner against a codex that records the catalog
// path it was handed and copies the catalog while it runs. It returns the
// sandboxed home, the copied catalog and the recorded path.
func runFakeCodex(t *testing.T) (home, seen, recorded string) {
	t.Helper()
	skipWithoutShell(t)
	home = sandboxCodexHome(t)
	seen = filepath.Join(t.TempDir(), "seen.json")
	pathFile := filepath.Join(t.TempDir(), "path.txt")
	bundle := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(bundle, []byte(fakeCodexCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIZMAL_TEST_BUNDLE", bundle)
	script := `#!/bin/sh
case "$1 $2 $3" in "debug models --bundled") exec cat "$PRIZMAL_TEST_BUNDLE";; esac
if [ "$1" = "--version" ]; then echo codex-cli 0.160.1; exit 0; fi
for a in "$@"; do
  case "$a" in model_catalog_json=*) p="${a#model_catalog_json=}"; p="${p#\"}"; p="${p%\"}"; echo "$p" > "` + pathFile + `"; cp "$p" "` + seen + `";; esac
done
exit 0
`
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := (&Codex{}).Run("prizmal-flash", []launch.LaunchModel{{Name: "prizmal-flash"}}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, _ := os.ReadFile(pathFile)
	return home, seen, strings.TrimSpace(string(raw))
}

// TestCodexArgsPassNoProfile pins that the launch needs no profile file: every
// setting rides a -c override.
func TestCodexArgsPassNoProfile(t *testing.T) {
	args, err := (&Codex{}).args("prizmal-flash", "/tmp/catalog.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--profile") {
		t.Fatalf("args pass --profile: %v", args)
	}
	for _, want := range []string{`model_provider="prizmal"`, `model_catalog_json="/tmp/catalog.json"`} {
		if !codexHasOverride(args, want) {
			t.Errorf("args lack -c %s: %v", want, args)
		}
	}
}

// TestCodexLaunchLeavesNothingUnderTheCodexDirectory pins the ephemeral launch:
// Codex sees the catalog while it runs, and neither the catalog nor a profile
// file is left in ~/.codex afterwards.
func TestCodexLaunchLeavesNothingUnderTheCodexDirectory(t *testing.T) {
	home, seen, catalog := runFakeCodex(t)
	if _, err := os.Stat(seen); err != nil {
		t.Fatalf("Codex never saw a catalog file: %v", err)
	}
	if strings.HasPrefix(catalog, filepath.Join(home, ".codex")) {
		t.Errorf("catalog %s sits under the Codex directory", catalog)
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Errorf("catalog %s outlives the launch (stat err = %v)", catalog, err)
	}
	for _, name := range []string{"prizmal.config.toml", "model.json", "config.toml"} {
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); !os.IsNotExist(err) {
			t.Errorf("%s was written under ~/.codex (stat err = %v)", name, err)
		}
	}
}

// A plain run must leave nothing key-shaped in the home directory. The
// credential rides the child environment and dies with the process.
func TestCodexLeavesNoKeyOnDisk(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	home, seen, _ := runFakeCodex(t)
	internaltest.AssertNoKeyUnder(t, home, "codex")
	raw, _ := os.ReadFile(seen)
	if strings.Contains(string(raw), internaltest.FakeKeyAtRest) {
		t.Fatal("the key appears in the catalog Codex read")
	}
}

// CODEX_HOME moves the directory Codex reads, so a launch must not write to
// ~/.codex on its behalf either way.
func TestCodexLaunchIgnoresCodexHome(t *testing.T) {
	other := t.TempDir()
	t.Setenv("CODEX_HOME", other)
	home, _, catalog := runFakeCodex(t)
	if strings.HasPrefix(catalog, other) || strings.HasPrefix(catalog, home) {
		t.Errorf("catalog %s sits under a Codex home", catalog)
	}
}
