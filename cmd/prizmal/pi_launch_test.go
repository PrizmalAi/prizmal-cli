package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// Pi can refresh a device token through the extension its launch loads: the
// provider there resolves its credential by running `prizmal auth token` for
// every request. Its launch tests live here; the ones for the other harnesses
// live beside this file, one file per harness.

// TestHarnessLaunchPi launches the real Pi through prizmal against the stub
// switch, on the switch key in the config file.
func TestHarnessLaunchPi(t *testing.T) { runHarnessLaunch(t, "pi", harnessLaunchConfigKey) }

// TestHarnessLaunchPiFromDeviceLogin pins that a device-login launch of Pi
// starts and authenticates. With a device key enrolled and a stub that accepts
// only the device token its own refresh handed out, the stub's reply on stdout
// proves Pi ran in device mode on a token the helper minted, not on the switch
// key the config file held.
func TestHarnessLaunchPiFromDeviceLogin(t *testing.T) {
	runHarnessLaunch(t, "pi", harnessLaunchDeviceLogin)
}

// piDeviceTokenLife is how long the short-lived stub's device tokens last in
// TestHarnessLaunchPiOutlivesOneDeviceToken. It is far shorter than
// a token lifetime the helper halves into its reuse margin, so it serves a cached token only briefly and must
// refresh for each request, which is what lets a few seconds of session cross an
// expiry.
const piDeviceTokenLife = 2 * time.Second

// piToolTurnSleep is how long each stub-issued bash call sleeps. Pi runs bash
// between turns, so the session's requests are spread by at least this much,
// which puts a second request after the first token's life.
const piToolTurnSleep = 3 * time.Second

// TestHarnessLaunchPiOutlivesOneDeviceToken runs a real Pi session across the
// expiry of a device token. The stub mints a short-lived token on every
// refresh, refuses any token whose life has passed, and answers the first two
// turns with a bash call that sleeps. Pi makes a request per turn, so the
// session spans more than one token life. The test passes only when Pi
// presented a token minted after the first expired; it fails when Pi kept
// sending the first token, because the stub refuses it with 401 and Pi stops.
func TestHarnessLaunchPiOutlivesOneDeviceToken(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	issuer := stubserver.NewDeviceTokenIssuer(piDeviceTokenLife)
	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithShortLivedDeviceTokens(issuer),
		stubserver.WithPiToolLoop(2, piToolTurnSleep),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, true)
	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "pi", harnessArgs)

	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
	if got := issuer.Issued(); got < 2 {
		t.Fatalf("the stub minted %d device token(s); the session never crossed an expiry", got)
	}
	if accepted := issuer.AcceptedValues(); len(accepted) < 2 {
		t.Fatalf("pi presented %d accepted device token(s) %v; it kept the first token instead of refreshing",
			len(accepted), accepted)
	}
	if !issuer.CrossedExpiry() {
		t.Fatalf("no accepted request arrived after the first token's life passed; the session never crossed an expiry")
	}
	if n := issuer.RejectedExpired(); n > 0 {
		t.Fatalf("the stub refused %d expired token(s); pi sent a token past its life instead of refreshing", n)
	}
}

// piUserExtensionFileName is the name of the user extension
// TestHarnessLaunchPiKeepsTheUsersExtensions plants in Pi's agent directory.
// The extension writes a marker file when its factory runs, so the test can
// tell whether Pi loaded it.
const piUserExtensionFileName = "prizmal-user-probe.ts"

// TestHarnessLaunchPiKeepsTheUsersExtensions pins that a device-login launch
// leaves the user's own extensions loaded. Pi's --extension adds to discovery
// rather than replacing it, so an extension in the user's agent directory still
// loads beside the one the launch adds. A user who has extensions of their own
// must not lose them to a prizmal launch.
func TestHarnessLaunchPiKeepsTheUsersExtensions(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithDeviceTokenOnly(),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, true)

	// A user extension in Pi's own agent directory, the place a person's global
	// extensions load from. Its factory appends to a marker file beside it, so
	// the test knows whether Pi loaded it.
	logPath := filepath.Join(home, "user-extension.log")
	userExtension := `import { appendFileSync } from "node:fs";
export default function (pi) {
  appendFileSync(` + strconv.Quote(logPath) + `, "user extension loaded\n");
}
`
	agentExtensions := filepath.Join(home, ".pi", "agent", "extensions")
	if err := os.MkdirAll(agentExtensions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentExtensions, piUserExtensionFileName), []byte(userExtension), 0o600); err != nil {
		t.Fatal(err)
	}

	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "pi", harnessArgs)

	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
	marker, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the user's extension did not run: %v\n--- stderr ---\n%s", err, tail(stderr, 30))
	}
	if !strings.Contains(string(marker), "user extension loaded") {
		t.Fatalf("the user's extension ran but wrote nothing: %q", marker)
	}
}

// TestHarnessLaunchPiWithoutAModel runs Pi's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch sends the config
// the stub's switch key is bound to.
func TestHarnessLaunchPiWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "pi")
}

// piTurnChargePico is the charge the stub reports for a turn: $0.04821.
const piTurnChargePico = 48210000000

// TestHarnessLaunchPiRecordsTheSwitchTurnCharge runs a real Pi against a stub
// that bills the turn, and reads the cost Pi recorded for it. Pi's footer sums
// that field. The stub's rates are not in models.json, so Pi alone records 0;
// the extension sets the figure the Switch sent.
func TestHarnessLaunchPiRecordsTheSwitchTurnCharge(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithTurnCharge(piTurnChargePico),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, false)
	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "pi", harnessArgs)
	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}

	var total float64
	found := false
	sessions, _ := filepath.Glob(filepath.Join(home, ".pi", "agent", "sessions", "*", "*.jsonl"))
	for _, path := range sessions {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			var entry struct {
				Message struct {
					Role  string `json:"role"`
					Usage struct {
						Cost struct {
							Total float64 `json:"total"`
						} `json:"cost"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Message.Role == "assistant" {
				total, found = entry.Message.Usage.Cost.Total, true
			}
		}
	}
	if !found {
		t.Fatalf("no assistant message in Pi's session files %v", sessions)
	}
	if want := float64(piTurnChargePico) / 1e12; total != want {
		t.Fatalf("Pi recorded a turn cost of %v, want the Switch's %v", total, want)
	}
}

// piSwitchModels is the catalog a Switch serves with names it decorates with
// the [1m] context-budget suffix. The launcher reads each entry's bare name.
var piSwitchModels = []string{"frontier", "core", "flash", "premier", "test"}

// TestHarnessLaunchPiListsEverySwitchModel pins both halves of the launch
// contract. A real Pi launched against a five-model stub must show all five in
// the list its own picker reads, and the launch must leave the operator's
// existing models.json entries exactly as they were: the entry for another
// provider and their own entry under the prizmal provider both survive, in
// place, with their fields unchanged.
func TestHarnessLaunchPiListsEverySwitchModel(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	srv := stubserver.NewServer(stubserver.WithModels(piSwitchModels...))
	t.Cleanup(srv.Close)
	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, false)

	// The operator's own config, in the shape prizmal finds on a machine it has
	// configured before: a second provider and a model prizmal does not manage.
	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userModels := `{
  "providers": {
    "openrouter": {
      "baseUrl": "https://openrouter.example.invalid/v1",
      "api": "openai-completions",
      "apiKey": "$OPERATOR_KEY",
      "models": [{"id": "zeta", "input": ["text"]}]
    },
    "prizmal": {
      "baseUrl": "https://switch.example.invalid/v1",
      "api": "openai-completions",
      "apiKey": "$PRIZMAL_SWITCH_KEY",
      "models": [{"id": "operator-model", "input": ["text"], "contextWindow": 65536}]
    }
  }
}
`
	if err := os.WriteFile(modelsPath, []byte(userModels), 0o600); err != nil {
		t.Fatal(err)
	}
	userSettings := "{\n  \"theme\": \"dark\"\n}\n"
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(userSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeProvider := renderedJSON(t, piProviderFromFile(t, modelsPath, "openrouter"))

	const launched = "flash"
	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath,
		append([]string{"-y", "-m", launched, "pi", "--"}, harnessArgs...)...)
	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}

	// Pi's picker offers the models its provider entry lists, so that entry is
	// the list under test.
	provider := piProviderFromFile(t, modelsPath, "prizmal")
	entries, _ := provider["models"].([]any)
	if len(entries) != len(piSwitchModels)+1 {
		t.Fatalf("prizmal provider lists %d entries, want %d Switch models plus the operator's: %v",
			len(entries), len(piSwitchModels), entries)
	}
	if first, _ := entries[0].(map[string]any); first["id"] != "operator-model" {
		t.Fatalf("the operator's entry moved: %v", entries)
	}
	listed := map[string]bool{}
	for _, entry := range entries {
		modelObj, _ := entry.(map[string]any)
		id, _ := modelObj["id"].(string)
		listed[id] = true
	}
	for _, name := range piSwitchModels {
		if !listed[name] {
			t.Fatalf("prizmal provider does not list %q: %v", name, entries)
		}
	}
	if after := renderedJSON(t, piProviderFromFile(t, modelsPath, "openrouter")); after != beforeProvider {
		t.Fatalf("the operator's other provider changed\nbefore: %s\nafter:  %s", beforeProvider, after)
	}

	// The list Pi's picker reads is the list Pi prints, with the key the launch
	// puts in the child environment. This is the assertion a user's /model makes.
	rows := piListModels(t, harnessPath, home)
	for _, name := range piSwitchModels {
		if !slices.Contains(rows, "prizmal"+"\t"+name) {
			t.Fatalf("pi does not offer prizmal/%s; it lists %v", name, rows)
		}
	}
}

// piProviderFromFile reads one provider out of a models.json.
func piProviderFromFile(t *testing.T, path, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	providers, _ := config["providers"].(map[string]any)
	provider, ok := providers[id].(map[string]any)
	if !ok {
		t.Fatalf("no %q provider in %s", id, path)
	}
	return provider
}

// renderedJSON marshals a value with sorted field names, so two reads compare
// content rather than key order.
func renderedJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// piListModels asks the installed Pi which models its picker offers, as the
// provider/model pairs it prints, one per row. A launch's /model reads the same
// list.
func piListModels(t *testing.T, harnessPath, home string) []string {
	t.Helper()
	cmd := exec.Command(harnessPath, "--list-models")
	cmd.Env = append(homeEnv(home),
		"PATH="+filepath.Dir(harnessPath)+string(filepath.ListSeparator)+os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
		"PRIZMAL_SWITCH_KEY="+stubserver.StubKey,
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pi --list-models: %v\n%s", err, out)
	}
	var rows []string
	for _, line := range strings.Split(string(out), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, fields[0]+"\t"+fields[1])
	}
	return rows
}
