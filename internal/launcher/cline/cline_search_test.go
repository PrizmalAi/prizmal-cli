package cline

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

func fakeClinePluginInstall(t *testing.T, calls *int, installedSource *string) clinePluginInstall {
	t.Helper()
	return func(pluginRoot, source string) (string, error) {
		*calls++
		data, err := os.ReadFile(filepath.Join(source, "index.js"))
		if err != nil {
			return "", err
		}
		*installedSource = string(data)
		installPath := clineInstalledPluginPath(pluginRoot, source)
		if err := os.MkdirAll(filepath.Join(installPath, "package"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(installPath, "package", "index.js"), data, 0o644); err != nil {
			return "", err
		}
		owner, err := os.ReadFile(clineSearchOwnerPath(source))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(clineSearchOwnerPath(installPath), owner, 0o644); err != nil {
			return "", err
		}
		return installPath, nil
	}
}

func TestClineSearchInstallRewriteAndRestoreOwnership(t *testing.T) {
	internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	dataDir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dataDir)
	t.Setenv("CLINE_DIR", "")

	userInstall := filepath.Join(dataDir, "plugins", "_installed", "local", "prizmal_search-user")
	if err := os.MkdirAll(userInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(userInstall, "user.txt")
	userBody := []byte("user-installed plugin; preserve every byte\n")
	if err := os.WriteFile(userFile, userBody, 0o600); err != nil {
		t.Fatal(err)
	}

	calls := 0
	var installedSource string
	previousInstall := installClinePlugin
	installClinePlugin = fakeClinePluginInstall(t, &calls, &installedSource)
	previousUninstall := uninstallClinePlugin
	uninstallClinePlugin = func(_ string, installPath string) error {
		return os.RemoveAll(installPath)
	}
	t.Cleanup(func() {
		installClinePlugin = previousInstall
		uninstallClinePlugin = previousUninstall
	})

	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	pluginRoot, err := c.pluginRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeClineSearch(dataDir, pluginRoot, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("install calls = %d, want 1", calls)
	}
	if !strings.Contains(installedSource, `name: "prizmal_search__search"`) {
		t.Fatalf("installed tool does not declare prizmal_search__search")
	}
	for _, secret := range []string{internaltest.FakeKeyAtRest, envconfig.APIKey()} {
		if secret != "" && strings.Contains(installedSource, secret) {
			t.Fatal("the installed plugin contains the credential")
		}
	}

	// A second launch rewrites the generated source even after it was changed.
	sourceFile := filepath.Join(clineSearchSourceDir(pluginRoot), "index.js")
	if err := os.WriteFile(sourceFile, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeClineSearch(dataDir, pluginRoot, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("install calls = %d, want 2", calls)
	}
	if strings.Contains(installedSource, "tampered") || !strings.Contains(installedSource, `name: "prizmal_search__search"`) {
		t.Fatal("the second launch did not replace the plugin from freshly written source")
	}

	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clineSearchSourceDir(pluginRoot)); !os.IsNotExist(err) {
		t.Fatalf("generated search source survived restore: %v", err)
	}
	if _, err := os.Stat(clineSearchStatePath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("search state survived restore: %v", err)
	}
	got, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("user plugin was removed: %v", err)
	}
	if string(got) != string(userBody) {
		t.Fatalf("user plugin changed: %q", got)
	}
}

func TestClineSearchRefusesToReplaceAnUnownedGeneratedPath(t *testing.T) {
	internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	dataDir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dataDir)
	t.Setenv("CLINE_DIR", "")

	calls := 0
	var installedSource string
	previousInstall := installClinePlugin
	installClinePlugin = fakeClinePluginInstall(t, &calls, &installedSource)
	t.Cleanup(func() { installClinePlugin = previousInstall })

	pluginRoot, err := clinePluginRoot(nil, os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	source := clineSearchSourceDir(pluginRoot)
	collision := clineInstalledPluginPath(pluginRoot, source)
	if err := os.MkdirAll(collision, 0o755); err != nil {
		t.Fatal(err)
	}
	userBody := []byte("not prizmal's\n")
	if err := os.WriteFile(filepath.Join(collision, "user.txt"), userBody, 0o600); err != nil {
		t.Fatal(err)
	}

	err = writeClineSearch(dataDir, pluginRoot, true)
	if err == nil || !strings.Contains(err.Error(), "without prizmal's owner marker") {
		t.Fatalf("writeClineSearch = %v, want an ownership refusal", err)
	}
	if calls != 0 {
		t.Fatalf("install calls = %d, want 0", calls)
	}
	got, err := os.ReadFile(filepath.Join(collision, "user.txt"))
	if err != nil || string(got) != string(userBody) {
		t.Fatalf("unowned plugin changed: %q, %v", got, err)
	}
}

func TestClineSearchEnvironmentFollowsRelayAndDirectMode(t *testing.T) {
	internaltest.SandboxedHome(t)
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_DIR", "")
	envconfig.SetBaseURL("https://switch.example.test")
	envconfig.SetAPIKey("invented-direct-key")
	t.Cleanup(func() {
		envconfig.ClearRelay()
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})

	c := &Cline{}
	direct := c.childEnvForModel("probe-model", mustPluginRoot(t, c))
	if got := internaltest.EnvValue(direct, clineSearchURLEnv+"="); got != "https://switch.example.test/v1" {
		t.Fatalf("%s = %q, want the direct Switch base", clineSearchURLEnv, got)
	}
	if got := internaltest.EnvValue(direct, "OPENAI_API_KEY="); got != "invented-direct-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want the direct launch key", got)
	}
	if got := envvalue(direct, clineSearchModelEnv); got != "probe-model" {
		t.Fatalf("%s = %q, want probe-model", clineSearchModelEnv, got)
	}

	envconfig.SetRelay("http://127.0.0.1:43210/v1", "invented-placeholder")
	relay := c.childEnvForModel("probe-model", mustPluginRoot(t, c))
	if got := internaltest.EnvValue(relay, clineSearchURLEnv+"="); got != "http://localhost:43210/v1" {
		t.Fatalf("%s = %q, want the loopback base", clineSearchURLEnv, got)
	}
	if got := internaltest.EnvValue(relay, "OPENAI_API_KEY="); got != "invented-placeholder" {
		t.Fatalf("OPENAI_API_KEY = %q, want the placeholder", got)
	}
}

func mustPluginRoot(t *testing.T, c *Cline) string {
	t.Helper()
	root, err := c.pluginRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func envvalue(env []string, name string) string {
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, name+"="); ok {
			return value
		}
	}
	return ""
}

// TestClineSearchStateIsKeyless keeps a future state field from writing the
// launch credential into Cline's data tree.
func TestClineSearchStateIsKeyless(t *testing.T) {
	internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, "invented-key-not-for-disk")
	dataDir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dataDir)
	t.Setenv("CLINE_DIR", "")
	calls := 0
	var installedSource string
	previousInstall := installClinePlugin
	installClinePlugin = fakeClinePluginInstall(t, &calls, &installedSource)
	t.Cleanup(func() { installClinePlugin = previousInstall })
	pluginRoot := mustPluginRoot(t, &Cline{})
	if err := writeClineSearch(dataDir, pluginRoot, true); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "invented-key-not-for-disk") {
			t.Fatalf("%s contains the launch credential", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Guard the generated owner marker against silently losing the JSON shape
// Restore uses to decide ownership.
func TestClineSearchOwnerMarkerIsStructured(t *testing.T) {
	data, err := json.Marshal(clineSearchOwner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"prizmal_search"`) ||
		!strings.Contains(string(data), `"tool":"prizmal_search__search"`) {
		t.Fatalf("owner marker = %s", data)
	}
}

func TestClineSearchInstallUsesClinePluginInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cline is a shell script")
	}
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture")
	pluginRoot := filepath.Join(t.TempDir(), "cline-root")
	source := filepath.Join(t.TempDir(), "plugin-source")
	installPath := filepath.Join(pluginRoot, "plugins", "_installed", "local", "probe")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + capture + "\n" +
		"printf '%s' \"$CLINE_DIR\" >> " + capture + "\n" +
		"printf '%s' '{\"installPath\":\"" + installPath + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "cline"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := runClinePluginInstall(pluginRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	if got != installPath {
		t.Fatalf("installPath = %q, want %q", got, installPath)
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := "plugin\ninstall\n--force\n--json\n" + source + "\n" + pluginRoot
	if string(args) != want {
		t.Fatalf("cline command = %q, want %q", args, want)
	}
}
