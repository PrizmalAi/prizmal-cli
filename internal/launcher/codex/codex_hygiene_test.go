package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// fakeBin puts executable shell scripts named after the keys of scripts on a
// PATH that holds nothing else.
func fakeBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	launch.SetConfirmPolicy(true)
	t.Cleanup(func() { launch.SetConfirmPolicy(false) })
	return dir
}

func TestEnsureCodexInstalledRunsNpmWhenMissing(t *testing.T) {
	skipWithoutShell(t)
	dir := fakeBin(t, nil)
	log := filepath.Join(dir, "npm.log")
	script := `echo "$@" > ` + log + "\n" + `printf '#!/bin/sh\necho codex-cli 0.163.0\n' > ` + filepath.Join(dir, "codex") + "\n" + `/bin/chmod +x ` + filepath.Join(dir, "codex")
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCodexInstalled(); err != nil {
		t.Fatalf("ensureCodexInstalled: %v", err)
	}
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "install -g @openai/codex" {
		t.Fatalf("npm ran with %q", got)
	}
}

func TestEnsureCodexInstalledWithoutNpmNamesTheDependency(t *testing.T) {
	fakeBin(t, nil)
	err := EnsureCodexInstalled()
	if err == nil || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("error = %v, want a message naming npm", err)
	}
}

// TestEnsureCodexInstalledReportsABinaryStillNotOnPath: npm exits 0 without
// putting codex anywhere, so the launch says so and names the fix rather than
// exec'ing a command that is not there.
func TestEnsureCodexInstalledReportsABinaryStillNotOnPath(t *testing.T) {
	skipWithoutShell(t)
	dir := fakeBin(t, map[string]string{"npm": "exit 0"})
	if err := EnsureCodexInstalled(); err == nil {
		t.Fatal("ensureCodexInstalled = nil, want an error naming PATH")
	} else if !strings.Contains(err.Error(), "was not found on PATH") {
		t.Fatalf("error = %v, want it to name PATH", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex")); !os.IsNotExist(err) {
		t.Fatalf("npm wrote a codex binary: %v", err)
	}
}

func TestEnsureCodexInstalledSkipsNpmWhenPresent(t *testing.T) {
	skipWithoutShell(t)
	fakeBin(t, map[string]string{"codex": "exit 0", "npm": "exit 1"})
	if err := EnsureCodexInstalled(); err != nil {
		t.Fatalf("ensureCodexInstalled: %v", err)
	}
}

func TestCodexChildEnvDropsInheritedVariables(t *testing.T) {
	envconfig.SetAPIKey("sk-switch")
	t.Cleanup(func() { envconfig.SetAPIKey("") })
	for _, name := range codexInheritedVars {
		t.Setenv(name, "stale")
	}
	t.Setenv("OPENAI_API_KEY", "sk-stale-openai")
	t.Setenv("KEEP_ME", "yes")

	env := codexChildEnv()
	for _, name := range codexInheritedVars {
		if launchtest.EnvValue(env, name+"=") != "" {
			t.Errorf("%s reached the child", name)
		}
	}
	if got := launchtest.EnvValue(env, "OPENAI_API_KEY="); got != "sk-switch" {
		t.Errorf("OPENAI_API_KEY = %q, want the switch key", got)
	}
	if n := countEnv(env, "OPENAI_API_KEY="); n != 1 {
		t.Errorf("OPENAI_API_KEY appears %d times", n)
	}
	if launchtest.EnvValue(env, "KEEP_ME=") != "yes" {
		t.Error("an unrelated variable was dropped")
	}
}

func TestCodexInheritedVarsCoverTheRedirectingOnes(t *testing.T) {
	for _, want := range []string{"OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "CODEX_HOME"} {
		if !slices.Contains(codexInheritedVars, want) {
			t.Errorf("codexInheritedVars lacks %s", want)
		}
	}
}

func TestCodexArgsTurnOffAnalyticsAndFeedback(t *testing.T) {
	args, err := (&Codex{}).args("prizmal-flash", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"analytics.enabled=false", "feedback.enabled=false"} {
		if !codexHasOverride(args, want) {
			t.Errorf("args lack -c %s: %v", want, args)
		}
	}
}

// --subagent-model names the model a spawned subagent runs and the model
// /review runs, the two slots Codex reads for delegated work.
func TestCodexArgsMapSubagentModelToBothDelegatedSlots(t *testing.T) {
	launch.SetSubagentModel("prizmal-core")
	t.Cleanup(func() { launch.SetSubagentModel("") })
	args, err := (&Codex{}).args("prizmal-flash", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`agents.default_subagent_model="prizmal-core"`,
		`review_model="prizmal-core"`,
	} {
		if !codexHasOverride(args, want) {
			t.Errorf("args lack %s: %v", want, args)
		}
	}
}

func TestCodexArgsOmitTheDelegatedModelsByDefault(t *testing.T) {
	launch.SetSubagentModel("")
	args, _ := (&Codex{}).args("prizmal-flash", "", nil)
	joined := strings.Join(args, " ")
	for _, name := range []string{"review_model", "agents.default_subagent_model"} {
		if strings.Contains(joined, name) {
			t.Errorf("%s set without --subagent-model: %v", name, args)
		}
	}
}

func codexHasOverride(args []string, override string) bool {
	for i, a := range args {
		if a == "-c" && i+1 < len(args) && args[i+1] == override {
			return true
		}
	}
	return false
}

func countEnv(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

// skipWithoutShell skips a test that stands in for a binary with a shell
// script, which Windows cannot run as an executable.
func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-ins do not run on Windows")
	}
}

// Codex spawns subagents on agents.default_subagent_model, a setting apart
// from review_model, so --subagent-model sets both.
func TestCodexArgsMapSubagentModelToAgentsDefault(t *testing.T) {
	launch.SetSubagentModel("prizmal-core")
	t.Cleanup(func() { launch.SetSubagentModel("") })
	args, err := (&Codex{}).args("prizmal-flash", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !codexHasOverride(args, `agents.default_subagent_model="prizmal-core"`) {
		t.Fatalf("args lack agents.default_subagent_model: %v", args)
	}
}

func TestCodexArgsOmitAgentsDefaultByDefault(t *testing.T) {
	launch.SetSubagentModel("")
	args, _ := (&Codex{}).args("prizmal-flash", "", nil)
	if strings.Contains(strings.Join(args, " "), "default_subagent_model") {
		t.Fatalf("default_subagent_model set without --subagent-model: %v", args)
	}
}

// Codex checks the subagent model against the models its catalog lists, so a
// model the tenant list lacks still gets an entry.
func TestCodexCatalogListsASubagentModelTheTenantListLacks(t *testing.T) {
	launch.SetSubagentModel("prizmal-core")
	t.Cleanup(func() { launch.SetSubagentModel("") })
	catalog := []launch.LaunchModel{{Name: "prizmal-flash"}}

	var slugs []string
	for _, m := range codexCatalogModels("prizmal-flash", catalog) {
		slugs = append(slugs, m.Name)
	}
	if strings.Join(slugs, ",") != "prizmal-flash,prizmal-core" {
		t.Fatalf("catalog = %v, want the subagent model listed after the launched one", slugs)
	}
}

func TestCodexCatalogListsASubagentModelOnce(t *testing.T) {
	launch.SetSubagentModel("prizmal-core")
	t.Cleanup(func() { launch.SetSubagentModel("") })
	catalog := []launch.LaunchModel{{Name: "prizmal-flash"}, {Name: "prizmal-core"}}

	if got := len(codexCatalogModels("prizmal-flash", catalog)); got != 2 {
		t.Fatalf("catalog has %d models, want 2", got)
	}
}
