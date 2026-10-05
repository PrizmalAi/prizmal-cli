package launch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

func TestCodexIntegrationIsAutoInstallable(t *testing.T) {
	spec := integrationSpecsByName["codex"]
	if spec == nil || spec.Install.EnsureInstalled == nil {
		t.Fatal("the codex integration has no EnsureInstalled")
	}
}

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
	SetConfirmPolicy(true)
	t.Cleanup(func() { SetConfirmPolicy(false) })
	return dir
}

func TestEnsureCodexInstalledRunsNpmWhenMissing(t *testing.T) {
	dir := fakeBin(t, nil)
	log := filepath.Join(dir, "npm.log")
	script := `echo "$@" > ` + log + "\n" + `printf '#!/bin/sh\necho codex-cli 0.160.0\n' > ` + filepath.Join(dir, "codex") + "\n" + `/bin/chmod +x ` + filepath.Join(dir, "codex")
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureCodexInstalled(); err != nil {
		t.Fatalf("ensureCodexInstalled: %v", err)
	}
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "install -g @openai/codex" {
		t.Fatalf("npm ran with %q", got)
	}
}

func TestEnsureCodexInstalledWithoutNpmNamesTheDependency(t *testing.T) {
	fakeBin(t, nil)
	err := ensureCodexInstalled()
	if err == nil || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("error = %v, want a message naming npm", err)
	}
}

func TestEnsureCodexInstalledSkipsNpmWhenPresent(t *testing.T) {
	fakeBin(t, map[string]string{"codex": "exit 0", "npm": "exit 1"})
	if err := ensureCodexInstalled(); err != nil {
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
		if envValue(env, name+"=") != "" {
			t.Errorf("%s reached the child", name)
		}
	}
	if got := envValue(env, "OPENAI_API_KEY="); got != "sk-switch" {
		t.Errorf("OPENAI_API_KEY = %q, want the switch key", got)
	}
	if n := countEnv(env, "OPENAI_API_KEY="); n != 1 {
		t.Errorf("OPENAI_API_KEY appears %d times", n)
	}
	if envValue(env, "KEEP_ME=") != "yes" {
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

func TestCodexArgsMapSubagentModelToReviewModel(t *testing.T) {
	SetSubagentModel("prizmal-core")
	t.Cleanup(func() { SetSubagentModel("") })
	args, err := (&Codex{}).args("prizmal-flash", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !codexHasOverride(args, `review_model="prizmal-core"`) {
		t.Fatalf("args lack review_model: %v", args)
	}
}

func TestCodexArgsOmitReviewModelByDefault(t *testing.T) {
	SetSubagentModel("")
	args, _ := (&Codex{}).args("prizmal-flash", "", nil)
	if strings.Contains(strings.Join(args, " "), "review_model") {
		t.Fatalf("review_model set without --subagent-model: %v", args)
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
