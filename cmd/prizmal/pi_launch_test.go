package main

import (
	"encoding/json"
	"os"
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

// TestHarnessLaunchPiListsEverySwitchModel pins the launch contract: a real Pi
// launched against a five-model stub lists all five in the list its own picker
// reads, keeps the operator's own model under the prizmal provider beside them,
// and leaves the operator's models.json and settings.json byte for byte as they
// were. The model list comes from the extension the launch loads, not from a
// file the launch wrote. The operator's other provider does not appear in a
// launch's list: its key is not in the launch environment, so Pi reports it
// unconfigured, and their file is what the byte comparison below protects.
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

	const launched = "flash"
	stdout, stderr, _, runErr := runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath,
		"-y", "-m", launched, "pi", "--", "--list-models")
	if runErr != nil {
		t.Fatalf("pi --list-models through prizmal: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, tail(stdout, 50), tail(stderr, 50))
	}

	rows := piModelRows(t, stdout)
	for _, name := range piSwitchModels {
		if !slices.Contains(rows, "prizmal\t"+name) {
			t.Fatalf("pi does not offer prizmal/%s; it lists %v", name, rows)
		}
	}
	if !slices.Contains(rows, "prizmal\toperator-model") {
		t.Fatalf("pi dropped the operator's own model under the prizmal provider; it lists %v", rows)
	}

	for path, before := range map[string][]byte{modelsPath: []byte(userModels), settingsPath: []byte(userSettings)} {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s changed across the launch\nbefore: %s\nafter:  %s", path, before, after)
		}
	}
}

// TestHarnessLaunchPiLeavesTheUserConfigByteIdentical runs a real session, on a
// switch key and on a device login, and pins the acceptance criterion both
// times: the operator's models.json and settings.json are byte-identical before
// and after, with no --persist anywhere.
func TestHarnessLaunchPiLeavesTheUserConfigByteIdentical(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")
	harnessArgs := harnessLaunchCases["pi"].args

	modelsJSON := `{"providers":{"prizmal":{"baseUrl":"https://switch.example.invalid/v1",` +
		`"api":"openai-completions","apiKey":"$PRIZMAL_SWITCH_KEY",` +
		`"models":[{"id":"operator-model","input":["text"],"contextWindow":65536}]}}}`
	settingsJSON := "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"prizmal\",\n  \"defaultModel\": \"operator-model\"\n}\n"

	for _, mode := range []struct {
		name    string
		device  bool
		srvOpts []stubserver.ServerOption
	}{
		{name: "switch key"},
		{name: "device login", device: true, srvOpts: []stubserver.ServerOption{stubserver.WithDeviceTokenOnly()}},
	} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			srvOpts := append([]stubserver.ServerOption{stubserver.WithModels(harnessLaunchModel)}, mode.srvOpts...)
			srv := stubserver.NewServer(srvOpts...)
			t.Cleanup(srv.Close)
			prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, mode.device)

			agentDir := filepath.Join(home, ".pi", "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			paths := map[string]string{
				filepath.Join(agentDir, "models.json"):   modelsJSON,
				filepath.Join(agentDir, "settings.json"): settingsJSON,
			}
			for path, content := range paths {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			stdout, stderr, timedOut, runErr := runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath,
				append([]string{"-y", "-m", harnessLaunchModel, "pi", "--"}, harnessArgs...)...)
			if !strings.Contains(stdout, stubserver.Reply) {
				t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
					runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
			}

			for path, before := range paths {
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != before {
					t.Fatalf("%s changed across the launch\nbefore: %s\nafter:  %s", path, before, after)
				}
			}
		})
	}
}

// piModelRows reads the provider/model pairs out of `pi --list-models` output,
// one per row. A launch's /model reads the same list.
func piModelRows(t *testing.T, out string) []string {
	t.Helper()
	var rows []string
	for _, line := range strings.Split(out, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, fields[0]+"\t"+fields[1])
	}
	return rows
}

// TestPersistPiWritesTheProviderAndTheDefault is the explicit persistent form:
// `prizmal --persist pi` writes the provider into the user's models.json and
// names it with the model in settings.json, and what it writes needs no
// variable beyond the switch key's, which the README documents.
func TestPersistPiWritesTheProviderAndTheDefault(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	srv := stubserver.NewServer(stubserver.WithModels(harnessLaunchModel))
	t.Cleanup(srv.Close)
	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, false)

	stdout, stderr, _, runErr := runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath,
		"-y", "-m", harnessLaunchModel, "--persist", "pi")
	if runErr != nil {
		t.Fatalf("prizmal --persist pi: %v\n--- stdout ---\n%s\n--- stderr ---\n%s", runErr, stdout, stderr)
	}

	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	models, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatalf("the persist wrote no models.json: %v", err)
	}
	if !strings.Contains(string(models), `"apiKey": "$PRIZMAL_SWITCH_KEY"`) {
		t.Fatalf("models.json does not name the switch key's variable:\n%s", models)
	}
	if strings.Contains(string(models), "PRIZMAL_CLIENT_VERSION") {
		t.Fatalf("models.json needs a variable the operator does not have:\n%s", models)
	}

	settings, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("the persist wrote no settings.json: %v", err)
	}
	var parsed struct {
		DefaultProvider string `json:"defaultProvider"`
		DefaultModel    string `json:"defaultModel"`
	}
	if err := json.Unmarshal(settings, &parsed); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if parsed.DefaultProvider != "prizmal" || parsed.DefaultModel != harnessLaunchModel {
		t.Fatalf("settings.json names %q/%q, want prizmal/%s", parsed.DefaultProvider, parsed.DefaultModel, harnessLaunchModel)
	}
}
