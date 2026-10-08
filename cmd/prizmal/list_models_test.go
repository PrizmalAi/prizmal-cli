package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// deployedCatalog is what the Switch serves on GET /v1/models, trimmed to the
// fields this feature reads. Every id carries the [1m] decoration the Switch
// adds, and the bare spelling rides beside it — the two are deliberately
// different here, because telling them apart is the point of the flag.
const deployedCatalog = `{
  "object": "list",
  "data": [
    {"id": "default[1m]", "name": "default", "display_name": "default", "owned_by": "prizmal.ai"},
    {"id": "cheap[1m]", "name": "cheap", "display_name": "cheap", "owned_by": "prizmal.ai", "tier": "haiku"},
    {"id": "opus[1m]", "name": "opus", "display_name": "opus", "owned_by": "prizmal.ai", "tier": "opus"}
  ],
  "extra_fields": {"request_type": "", "latency": 0}
}`

// tenantCatalogServer stands in for the Switch: it answers the catalog only to
// a caller that presents a bearer token, so a keyless run cannot pass by
// accident.
func tenantCatalogServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"API key is required"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runPrizmalList runs prizmal as a subprocess against a temp HOME. Neither
// PRIZMAL_SWITCH_URL nor PRIZMAL_SWITCH_KEY is set, so the config file and the
// flags are the only credential sources the child can see. A bin/ under home is
// put on PATH when it exists, so a test can plant an executable probe there.
func runPrizmalList(t *testing.T, prizmalBin, home string, args ...string) (int, string, string) {
	t.Helper()

	path := os.Getenv("PATH")
	if binDir := filepath.Join(home, "bin"); dirExists(binDir) {
		path = binDir + string(filepath.ListSeparator) + path
	}

	cmd := exec.Command(prizmalBin, args...)
	cmd.Stdin = nil
	cmd.Env = prizmalChildEnv(home, path)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run prizmal: %v", err)
		}
		exitCode = exitErr.ExitCode()
	}
	return exitCode, stdout.String(), stderr.String()
}

// prizmalChildEnv is the minimal environment a prizmal subprocess needs to
// find its config under home. Windows resolves the home directory from
// USERPROFILE, not HOME, so both are set there.
func prizmalChildEnv(home, path string) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + path,
		"PRIZMAL_ENV=testing",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home)
	}
	return env
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// deployedListing is what `prizmal --list` prints for deployedCatalog: the four
// Claude tier aliases first, because they route even though the Switch does not
// list them, then the Switch's own entries in its order. The alias lines are
// the point of the expectation — a --list that dropped them would under-report
// what `prizmal --model claude-tier-opus claude` launches.
const deployedListing = "claude-tier-opus\nclaude-tier-haiku\ndefault\ncheap\nopus\n"

// claudeTiers are the alias names withClaudeTiers supplies, in tierWords order.
// They are pinned here rather than imported because the point of the test is
// what the operator sees, and the operator sees these spellings. The tenant
// holds the opus and haiku tiers.
var claudeTiers = []string{"claude-tier-opus", "claude-tier-haiku"}

// TestListFlagPrintsTheTenantsModels is the feature: `--list` answers with the
// names the switch key's tenant can route by, one per line on stdout, so the
// output pipes without a header in the way.
func TestListFlagPrintsTheTenantsModels(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("prizmal --list exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if got := stdout; got != deployedListing {
		t.Errorf("stdout = %q, want %q", got, deployedListing)
	}
}

// The four tier aliases lead the listing, and they lead it in tierWords order,
// because `--pick` leads with them in that order. Two flags answering one
// question out of order is the drift this change exists to stop.
func TestListFlagLeadsWithTheClaudeTiers(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("prizmal --list exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) < len(claudeTiers) {
		t.Fatalf("stdout carried %d lines, want at least the %d tiers\nstdout: %q", len(lines), len(claudeTiers), stdout)
	}
	for i, want := range claudeTiers {
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q; --pick leads with these in this order", i+1, lines[i], want)
		}
	}
}

// The names printed are the bare ones a caller passes back as --model. The
// Switch decorates every id with Claude Code's [1m] suffix, and a bare name is
// what routes for a harness that does not strip it.
func TestListFlagPrintsBareNamesNotDecoratedIDs(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "-l", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("prizmal -l exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if strings.Contains(stdout, "[1m]") {
		t.Errorf("stdout carries the [1m] decoration: %q", stdout)
	}
}

// The short form is the same command.
func TestListFlagShortFormMatchesLong(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	longCode, longOut, _ := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	shortCode, shortOut, _ := runPrizmalList(t, prizmalBin, home, "-l", "--url", srv.URL)
	if longCode != 0 || shortCode != 0 {
		t.Fatalf("--list exited %d, -l exited %d, want 0 for both", longCode, shortCode)
	}
	if shortOut != longOut {
		t.Errorf("-l produced %q, --list produced %q; the short form must be the same command", shortOut, longOut)
	}
}

// A keyless run at a remote host cannot read a tenant's models, and the Switch
// would answer 401 — which reads as an invalid credential rather than a missing
// one. The refusal names all three places a key can come from and never emits a
// key-shaped token.
func TestListFlagRefusesRemoteWithoutKey(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	home := t.TempDir()
	writePrizmalConfig(t, home, "")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", "https://switch.example.test")
	if exitCode == 0 {
		t.Fatalf("a keyless remote --list must fail loudly; exited 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	for _, want := range []string{"--api-key", "PRIZMAL_SWITCH_KEY", "config file"} {
		if !strings.Contains(stderr+stdout, want) {
			t.Errorf("refusal must name %q\nstdout: %s\nstderr: %s", want, stdout, stderr)
		}
	}
	if strings.Contains(stderr+stdout, "sk-") {
		t.Errorf("refusal output contains a key-shaped token\nstderr: %s", stderr)
	}
}

// A switch key bound to no router config lists nothing. That is a valid empty
// answer, not a failure, so the exit status is 0 — but it must not read as a
// successful listing that silently lost its rows.
func TestListFlagEmptyTenantSaysSo(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, `{"object":"list","data":[]}`, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("an empty listing exited %d, want 0\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want it empty", stdout)
	}
	if !strings.Contains(strings.ToLower(stderr), "no models") {
		t.Errorf("stderr = %q, want it to say the listing is empty", stderr)
	}
}

// A Switch that cannot answer must fail the command, not print a partial or
// empty list that a script would read as "this key routes to nothing".
func TestListFlagFailsWhenTheSwitchErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	if exitCode == 0 {
		t.Fatalf("a failed catalog fetch must exit non-zero\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want no listing from a failed fetch", stdout)
	}
}

// `--list` lists even when an integration word is present, and never launches
// it: the planted binary exits 3, which would be the command's exit status if a
// launch happened. The prizmal flag comes before the integration name, per the
// positional grammar.
func TestListFlagDoesNotLaunchAHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("probe binary is a POSIX shell script")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL, "claude")
	if exitCode != 0 {
		t.Fatalf("`prizmal --list claude` exited %d, want the listing without a launch\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if got := stdout; got != deployedListing {
		t.Errorf("stdout = %q, want the listing %q", got, deployedListing)
	}
}

// A listing is a read-only query whose output pipes into another command, so
// it must not narrate where the credential came from. A launch announces the
// source because the choice shapes the child process; a listing has nothing to
// debug, and the refusal path already names every source when a key is missing.
func TestListFlagDoesNotAnnounceTheKeySource(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, deployedCatalog, http.StatusOK)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-test-key")

	exitCode, stdout, stderr := runPrizmalList(t, prizmalBin, home, "--list", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("prizmal --list exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if strings.Contains(stderr, "using api key from") {
		t.Errorf("--list announced the key source on stderr: %q", stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want it empty for a clean listing", stderr)
	}
}
