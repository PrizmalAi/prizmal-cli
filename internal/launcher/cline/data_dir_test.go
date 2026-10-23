package cline

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

func TestClineDataDirPrecedence(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		want      string
		relocated bool
	}{
		{name: "data-dir flag", args: []string{"--config", "cfg", "--data-dir", "flag-data"}, env: map[string]string{"CLINE_DATA_DIR": "env-data", "CLINE_DIR": "cline-dir"}, want: "flag-data", relocated: true},
		{name: "data-dir inline", args: []string{"--data-dir=inline-data"}, want: "inline-data", relocated: true},
		{name: "data-dir env", args: []string{"--config", "cfg"}, env: map[string]string{"CLINE_DATA_DIR": "env-data", "CLINE_DIR": "cline-dir"}, want: "env-data", relocated: true},
		{name: "config flag", args: []string{"--config", "cfg"}, env: map[string]string{"CLINE_DIR": "cline-dir"}, want: filepath.Join("cfg", "data"), relocated: true},
		{name: "cline dir env", env: map[string]string{"CLINE_DIR": "cline-dir"}, want: filepath.Join("cline-dir", "data"), relocated: true},
		{name: "default", want: filepath.Join(home, ".cline", "data")},
		{name: "prompt text", args: []string{"--system", "--data-dir", "prompt"}, want: filepath.Join(home, ".cline", "data")},
		{name: "after separator", args: []string{"--", "--data-dir", "ignored"}, want: filepath.Join(home, ".cline", "data")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, relocated, err := clineDataDir(tt.args, func(name string) string { return tt.env[name] }, func() (string, error) { return home, nil })
			if err != nil {
				t.Fatal(err)
			}
			want := tt.want
			if tt.relocated {
				want, _ = filepath.Abs(want)
			}
			if got != want || relocated != tt.relocated {
				t.Fatalf("data dir = %q, relocated %v; want %q, relocated %v", got, relocated, want, tt.relocated)
			}
		})
	}
}

func TestClinePathsUseResolvedDataDir(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	t.Setenv("CLINE_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".cline", ".prizmal", "cline-search", clineSearchPluginName, "index.js"),
		filepath.Join(home, ".cline", ".prizmal", "cline-search", clineSearchPluginName, clineSearchOwnerFile),
		filepath.Join(dir, "globalState.json"),
		filepath.Join(dir, "settings", "providers.json"),
	}
	if got := c.Paths(); !slices.Equal(got, want) {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
}

func TestClineRunWritesDataDirFlagTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cline is a shell script")
	}
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_DIR", "")
	calls := 0
	var installedSource string
	previousInstall := installClinePlugin
	installClinePlugin = fakeClinePluginInstall(t, &calls, &installedSource)
	t.Cleanup(func() { installClinePlugin = previousInstall })
	bin := internaltest.WithEmptyPATH(t)
	args := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\n"
	if err := os.WriteFile(filepath.Join(bin, "cline"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	c := &Cline{}
	launchArgs := []string{"--data-dir", dir, "-y", "task"}
	if err := c.Run("probe-model", nil, launchArgs); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "settings", "providers.json"), filepath.Join(dir, "globalState.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, clineProvPath)); !os.IsNotExist(err) {
		t.Fatalf("default providers.json exists or cannot be checked: %v", err)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "--data-dir\n"+dir+"\n-y\ntask\n"; got != want {
		t.Fatalf("cline args = %q, want %q", got, want)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Run("probe-model", nil, launchArgs); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a second launch rewrote the same --data-dir tree")
	}
}

func TestClineDefaultDataDirKeepsChildEnvironmentUnchanged(t *testing.T) {
	internaltest.SandboxedHome(t)
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_DIR", "")
	if got := internaltest.EnvValue((&Cline{}).envVars(), "CLINE_DATA_DIR="); got != "" {
		t.Fatalf("CLINE_DATA_DIR = %q, want it absent", got)
	}
}

func TestClineRestoreUsesRelocatedTreeAndBackups(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	internaltest.SandboxedSeed(t, dir, filepath.Join("settings", "providers.json"), `{"version":1,"lastUsedProvider":"anthropic","providers":{"anthropic":{"settings":{"provider":"anthropic"}}}}`)
	internaltest.SandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"other","providers":{"other":{"settings":{"provider":"other"}}}}`)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	prov := internaltest.SandboxedJSON(t, dir, filepath.Join("settings", "providers.json"))
	if got := prov["lastUsedProvider"]; got != "anthropic" {
		t.Fatalf("relocated lastUsedProvider = %v, want anthropic", got)
	}
	defaultProv := internaltest.SandboxedJSON(t, home, clineProvPath)
	if got := defaultProv["lastUsedProvider"]; got != "other" {
		t.Fatalf("default lastUsedProvider = %v, want other", got)
	}
}
