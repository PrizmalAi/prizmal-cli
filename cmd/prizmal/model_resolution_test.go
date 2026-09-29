package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/spf13/pflag"
)

// catalogBody is the model list the stub switch answers with.
const catalogBody = `{"data":[{"id":"saved-model"},{"id":"picked-model"},{"id":"flag-model"}]}`

// writeConfig writes a ~/.prizmal/config.json under a temp HOME and returns the
// parsed config plus that HOME.
func writeConfig(t *testing.T, body map[string]any) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, config.PrizmalDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if body == nil {
		body = map[string]any{"version": 1}
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.PrizmalFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg, home
}

// readDefaultModel reads default_model straight from the config file, so a test
// sees what was persisted rather than what the in-memory value says.
func readDefaultModel(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, config.PrizmalDir, config.PrizmalFileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got struct {
		DefaultModel string `json:"default_model"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return got.DefaultModel
}

// useStubSwitch points prizmal at a stub switch serving the catalog, and clears
// the launch's cached list so the next fetch reads it. Pass a body to serve a
// different catalog.
func useStubSwitch(t *testing.T, body ...string) {
	t.Helper()
	served := catalogBody
	if len(body) > 0 {
		served = body[0]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(served))
	}))
	t.Cleanup(srv.Close)

	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey("test-switch-key")
	launcher.ResetModelCatalog()
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
		launcher.ResetModelCatalog()
	})
}

// useFailingSwitch points prizmal at a stub switch whose catalog endpoint
// fails, the way an endpoint with no /v1/models does.
func useFailingSwitch(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	t.Cleanup(srv.Close)

	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey("test-switch-key")
	launcher.ResetModelCatalog()
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
		launcher.ResetModelCatalog()
	})
}

// stubPicker replaces the interactive menu with one that returns a fixed model,
// and reports the rows it was offered.
func stubPicker(t *testing.T, chosen string, sawRows *[]string) {
	t.Helper()
	original := launcher.ModelPickerMenu
	t.Cleanup(func() { launcher.ModelPickerMenu = original })

	launcher.ModelPickerMenu = func(rows []launcher.ModelRow) (string, error) {
		if sawRows != nil {
			for _, row := range rows {
				*sawRows = append(*sawRows, row.Model)
			}
		}
		return chosen, nil
	}
}

// stubTerminal forces the interactive-terminal check for one test.
func stubTerminal(t *testing.T, interactive bool) {
	t.Helper()
	original := launcher.StdinIsTerminal
	t.Cleanup(func() { launcher.StdinIsTerminal = original })
	launcher.StdinIsTerminal = func() bool { return interactive }
}

// setModel sets the --model flag for one test and restores it.
func setModel(t *testing.T, value string) {
	t.Helper()
	original := model
	t.Cleanup(func() { model = original })
	model = value
}

// setPick sets the --pick flag for one test and restores it.
func setPick(t *testing.T, value bool) {
	t.Helper()
	original := pickFlag
	t.Cleanup(func() { pickFlag = original })
	pickFlag = value
}

// default_model is what a launch runs when --model names nothing.
func TestDefaultModelIsUsedWhenNoFlagIsGiven(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "")
	setPick(t, false)
	stubTerminal(t, false)

	chosen, _, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "saved-model" {
		t.Fatalf("resolved %q, want saved-model", chosen)
	}
	// Using the saved default must not rewrite it.
	if got := readDefaultModel(t, home); got != "saved-model" {
		t.Errorf("default_model = %q, want it unchanged", got)
	}
}

// --model always wins over the saved default.
func TestModelFlagOverridesDefaultModel(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "flag-model")
	setPick(t, false)

	chosen, _, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "flag-model" {
		t.Fatalf("resolved %q, want flag-model", chosen)
	}
	// A flag changes one launch, not what a bare launch will do next.
	if got := readDefaultModel(t, home); got != "saved-model" {
		t.Errorf("--model rewrote default_model to %q", got)
	}
}

// A pick is saved as the default, so the next bare launch repeats it.
func TestPickSavesTheChosenModelAsDefault(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked-model", nil)

	chosen, _, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want picked-model", chosen)
	}
	if got := readDefaultModel(t, home); got != "picked-model" {
		t.Fatalf("default_model = %q, want the pick to be saved", got)
	}
}

// --pick skips the saved default: asking to choose means choosing, not reusing.
// This is what makes `prizmal --pick claude` open the menu on a machine that
// already has a default.
func TestPickSkipsTheSavedDefault(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "")
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "fresh-choice", nil)

	chosen, _, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "fresh-choice" {
		t.Fatalf("resolved %q, want the fresh choice; --pick beats the saved default", chosen)
	}
	if got := readDefaultModel(t, home); got != "fresh-choice" {
		t.Fatalf("default_model = %q, want the pick saved", got)
	}
}

// pickDefaultModel is the --pick mode: choose, save, and exit without
// launching. It returns the chosen model for the caller to print.
func TestPickDefaultModelSavesAndReturnsTheChoice(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "old-model"})
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "fresh-choice", nil)

	if err := pickDefaultModel(cfg); err != nil {
		t.Fatalf("pickDefaultModel: %v", err)
	}
	if got := readDefaultModel(t, home); got != "fresh-choice" {
		t.Fatalf("default_model = %q, want the fresh choice saved over the old one", got)
	}
}

// A pick that saves reports what it saved and where, so the operator is not
// left guessing whether the menu's choice took effect.
func TestPickDefaultModelReportsWhatItSaved(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "chosen-model", nil)

	stderr := captureStream(t, &os.Stderr, func() {
		if err := pickDefaultModel(cfg); err != nil {
			t.Fatalf("pickDefaultModel: %v", err)
		}
	})

	if !strings.Contains(stderr, "Saved") {
		t.Errorf("no confirmation was printed\nstderr: %q", stderr)
	}
	if !strings.Contains(stderr, "chosen-model") {
		t.Errorf("the confirmation does not name the model it saved\nstderr: %q", stderr)
	}
	// The path matters: a bare "saved" leaves open where.
	if !strings.Contains(stderr, filepath.Join(home, config.PrizmalDir, config.PrizmalFileName)) {
		t.Errorf("the confirmation does not name the config file it wrote\nstderr: %q", stderr)
	}
}

// The confirmation goes to stderr, so stdout stays the machine-readable value a
// script captures. --list draws the same line for the same reason.
func TestPickDefaultModelKeepsStdoutMachineReadable(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "chosen-model", nil)

	stdout := captureStream(t, &os.Stdout, func() {
		if err := pickDefaultModel(cfg); err != nil {
			t.Fatalf("pickDefaultModel: %v", err)
		}
	})

	if stdout != "chosen-model\n" {
		t.Fatalf("stdout = %q, want just the model and a newline", stdout)
	}
}

// captureStream runs f with one of the process streams replaced by a pipe and
// returns what f wrote to it. The confirmation a pick prints is the mode's only
// visible output, so a test has to read the stream it lands on; the stream is a
// parameter rather than two near-identical helpers.
func captureStream(t *testing.T, stream **os.File, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := *stream
	*stream = w
	defer func() { *stream = original }()

	f()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(out)
}

// A pick with no config file has nowhere to save the choice, so the mode fails
// rather than silently doing nothing. Unlike a pick during a launch, where the
// model in hand is the point, this mode exists only to set the default.
func TestPickDefaultModelRefusesWithoutAConfig(t *testing.T) {
	useStubSwitch(t)
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked", nil)

	err := pickDefaultModel(nil)
	if err == nil {
		t.Fatal("--pick with no config file must fail rather than pretend to save")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error does not name the missing config file: %v", err)
	}
}

// The picker's own mode gates the credential before the fetch, like --list
// does, so a keyless remote run names the missing key rather than reporting a
// network failure the operator reads as a rejected key.
func TestPickDefaultModelGatesBeforeTheFetch(t *testing.T) {
	envconfig.SetBaseURL("https://switch.example.test")
	envconfig.SetAPIKey("")
	t.Setenv(envconfig.KeyEnvVar, "")
	launcher.ResetModelCatalog()
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
		launcher.ResetModelCatalog()
	})

	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked", nil)

	err := pickDefaultModel(cfg)
	if err == nil {
		t.Fatal("a keyless pick must be refused")
	}
	for _, want := range []string{"--api-key", envconfig.KeyEnvVar, "config file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %v", want, err)
		}
	}
}

// A failed catalog fetch stops the picker mode: with no list there is nothing
// to choose from, and the mode's whole job is the choice.
func TestPickDefaultModelFailsOnAnUnreadableCatalog(t *testing.T) {
	useFailingSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "kept"})
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked", nil)

	if err := pickDefaultModel(cfg); err == nil {
		t.Fatal("the picker mode must fail when the model list cannot be read")
	}
	// The old default must survive a failed pick.
	if got := readDefaultModel(t, home); got != "kept" {
		t.Fatalf("default_model = %q, want the failed pick to leave it alone", got)
	}
}

// --model still wins over --pick, so a scripted launch cannot be diverted into
// a menu.
// --model is a launch flag and --pick is a separate mode, so the two never
// meet inside a launch. A launch with --model and the pick flag set still runs
// the flag: the pick mode is handled before the launch begins.
func TestModelFlagIsUnambiguousInALaunch(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "flag-model")
	setPick(t, true)

	chosen, _, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "flag-model" {
		t.Fatalf("resolved %q, want flag-model", chosen)
	}
}

// With no model, no default, and no terminal, the launch stops with a message
// naming what to do. It must not hang waiting on a menu nobody can answer.
func TestNoModelAndNoTerminalFails(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, false)
	stubTerminal(t, false)

	_, _, err := resolveLaunchModel(cfg, "")
	if err == nil {
		t.Fatal("a launch with no model and no terminal must fail")
	}
	for _, want := range []string{"--model", "--pick", "default_model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

// The pick mode needs a terminal, and refuses without one rather than opening a
// menu nobody can answer.
func TestPickDefaultModelRefusesWithoutATerminal(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	stubTerminal(t, false)
	stubPicker(t, "picked", nil)

	err := pickDefaultModel(cfg)
	if err == nil {
		t.Fatal("--pick without a terminal must fail rather than hang")
	}
	if !strings.Contains(err.Error(), "--model") {
		t.Errorf("error does not offer --model as the way out: %v", err)
	}
}

// A bare launch on a terminal with no saved default opens the picker, which is
// the acceptance criterion for "no default and no flag".
func TestBareLaunchOnATerminalOpensThePicker(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, false)
	stubTerminal(t, true)

	var sawRows []string
	stubPicker(t, "chosen-here", &sawRows)

	chosen, catalog, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "chosen-here" {
		t.Fatalf("resolved %q, want chosen-here", chosen)
	}
	if len(sawRows) != 3 {
		t.Fatalf("the picker was offered %d rows (%v), want the whole 3-model catalog", len(sawRows), sawRows)
	}
	if len(catalog) != 3 {
		t.Fatalf("catalog has %d models, want 3", len(catalog))
	}
	if got := readDefaultModel(t, home); got != "chosen-here" {
		t.Fatalf("default_model = %q, want the bare launch's pick saved", got)
	}
}

// The reserved prizmal/default name is never resolved by any path: the switch
// name is being sunset, so a launch must carry a real model or stop.
func TestNoLaunchPathResolvesPrizmalDefault(t *testing.T) {
	useStubSwitch(t)

	for _, tc := range []struct {
		name  string
		flag  string
		saved string
	}{
		{name: "flag", flag: "flag-model"},
		{name: "saved default", saved: "saved-model"},
		{name: "picker", saved: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := writeConfig(t, map[string]any{"version": 1, "default_model": tc.saved})
			setModel(t, tc.flag)
			setPick(t, false)
			stubTerminal(t, true)
			stubPicker(t, "picked-model", nil)

			chosen, _, err := resolveLaunchModel(cfg, "")
			if err != nil {
				t.Fatalf("resolveLaunchModel: %v", err)
			}
			if strings.HasPrefix(chosen, "prizmal/") {
				t.Fatalf("resolved %q; the reserved name must never be sent", chosen)
			}
		})
	}
}

// A launch with no config file at all — a machine prizmal has never configured
// — still runs the model the operator picks, without a place to save it.
func TestPickWithoutAConfigStillLaunches(t *testing.T) {
	useStubSwitch(t)
	setModel(t, "")
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked-model", nil)

	chosen, _, err := resolveLaunchModel(nil, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel(nil, empty): %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want picked-model", chosen)
	}
}

// TestPickHasNoShorthand pins that --pick stays long-form only.
//
// -p is --print in Claude Code, the flag an operator reaches for on nearly
// every non-interactive run. Claiming it for --pick would turn
// `prizmal claude -p "prompt"` into a picker prompt instead of the print run
// they meant, and the substitution would be invisible until the menu appeared.
func TestPickHasNoShorthand(t *testing.T) {
	flags := pflag.NewFlagSet("prizmal", pflag.ContinueOnError)
	registerFlags(flags)

	pick := flags.Lookup("pick")
	if pick == nil {
		t.Fatal("--pick is not registered")
	}
	if pick.Shorthand != "" {
		t.Fatalf("--pick has the shorthand -%s\n-p belongs to the harness (Claude Code's --print), so claiming it changes what `prizmal claude -p` does", pick.Shorthand)
	}
}

// TestPickerRefusesAKeylessRemoteFetch is the credential gate's intent applied
// to the picker. The picker needs the model list, and the list needs a key on a
// remote switch. Without the gate, the fetch goes out unauthenticated, the
// switch answers 401, and the operator reads that as a bad key rather than a
// missing one. --list gates before the same fetch for exactly this reason.
func TestPickerRefusesAKeylessRemoteFetch(t *testing.T) {
	envconfig.SetBaseURL("https://switch.example.test")
	envconfig.SetAPIKey("")
	t.Setenv(envconfig.KeyEnvVar, "")
	launcher.ResetModelCatalog()
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
		launcher.ResetModelCatalog()
	})

	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked-model", nil)

	_, _, err := resolveLaunchModel(cfg, "")
	if err == nil {
		t.Fatal("a keyless picker run must be refused")
	}
	// The refusal must name the places a key comes from, which is what tells
	// the operator they have no key rather than a rejected one.
	for _, want := range []string{"--api-key", envconfig.KeyEnvVar, "config file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %v", want, err)
		}
	}
}

// A failed catalog fetch does not stop a launch that already has its model from
// --model: the list only adds capabilities and picker rows. The CLI points at
// arbitrary provider endpoints, and one serving no /v1/models is a supported
// target, so this degrades with a warning.
func TestCatalogFetchFailureDoesNotStopAModelFlagLaunch(t *testing.T) {
	useFailingSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "flag-model")
	setPick(t, false)

	chosen, catalog, err := resolveLaunchModel(cfg, "")
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "flag-model" {
		t.Fatalf("resolved %q, want flag-model", chosen)
	}
	if len(catalog) != 0 {
		t.Fatalf("catalog = %v, want empty after a failed fetch", catalog)
	}
}

// A failed catalog fetch stops the picker path, which cannot degrade: a menu
// with rows silently missing would offer choices the tenant may not serve.
func TestCatalogFetchFailureStopsThePicker(t *testing.T) {
	useFailingSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, true)
	stubTerminal(t, true)
	stubPicker(t, "picked-model", nil)

	_, _, err := resolveLaunchModel(cfg, "")
	if err == nil {
		t.Fatal("the picker must not open when the model list could not be read")
	}
	if !strings.Contains(err.Error(), "could not read models") {
		t.Errorf("error does not say the list could not be read: %v", err)
	}
}
