package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// useStubSwitch points prizmal at a stub switch serving the catalog. The launch
// keys its cached list on the endpoint and credential, so a fresh stub server
// per test is already a fresh fetch. Pass a body to serve a different catalog.
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
	// The credential is unique to this test. The catalog memo is keyed on the
	// endpoint and the key together, and httptest hands a freed port to a later
	// server, so two tests can otherwise compare equal and this one would be
	// answered from the earlier test's rows instead of reaching its own 500.
	envconfig.SetAPIKey("failing-switch-key-" + t.Name())
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
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
	// The catalog memo is shared process state: without this, a fetch another
	// test cached under the same endpoint and key would answer this one and the
	// 500 would never be reached.
	launcher.ResetModelCatalog()
	t.Cleanup(func() {
		launcher.ResetModelCatalog()
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})
}

// askWith is the Asking a test launches with: whether there is a person to
// answer, and the menu that asks them.
//
// chosen is the model the menu returns, and sawRows, when not nil, collects
// the rows the menu was offered. An empty chosen wires no menu at all, so a
// case that must never reach one says so by leaving it out: reaching it fails
// with the launcher's own message instead of quietly picking nothing.
func askWith(interactive bool, chosen string, sawRows *[]string) launcher.Asking {
	ask := launcher.Asking{Interactive: func() bool { return interactive }}
	if chosen == "" {
		return ask
	}
	ask.Menu = func(rows []launcher.ModelRow) (launcher.Selection, error) {
		if sawRows != nil {
			for _, row := range rows {
				*sawRows = append(*sawRows, row.Model)
			}
		}
		return launcher.Selection{Model: chosen}, nil
	}
	return ask
}

// askSaving is askWith for an operator who picks chosen with the menu's save
// shortcut.
func askSaving(chosen string) launcher.Asking {
	ask := askWith(true, chosen, nil)
	ask.Menu = func([]launcher.ModelRow) (launcher.Selection, error) {
		return launcher.Selection{Model: chosen, Save: true}, nil
	}
	return ask
}

// askNobody is the Asking for a launch that must not reach a person: no probe
// to pass and no menu to open.
var askNobody = askWith(false, "", nil)

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
	ask := askNobody

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
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

// A saved default_model of `default` is a placeholder, not a model name, and the
// CLI never sends it. A non-interactive launch that would fall back on it
// stops, and the error names the models this key routes.
func TestSavedDefaultOfDefaultIsNeverSent(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1, "default_model": "default"})
	setModel(t, "")
	setPick(t, false)

	chosen, _, err := resolveLaunchModel(cfg, "", askNobody, false)
	if err == nil {
		t.Fatalf("resolved %q, want the launch to stop", chosen)
	}
	for _, want := range []string{"no model selected", "saved-model", "picked-model", "flag-model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

// On a terminal the same saved default is ignored rather than sent: the
// picker asks, and the choice replaces it.
func TestSavedDefaultOfDefaultOpensThePicker(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "default"})
	setModel(t, "")
	setPick(t, false)
	var rows []string

	chosen, _, err := resolveLaunchModel(cfg, "", askWith(true, "picked-model", &rows), false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want the picker's choice", chosen)
	}
	if !slices.Contains(rows, "saved-model") {
		t.Errorf("the picker offered %v, want the catalog's rows", rows)
	}
	if got := readDefaultModel(t, home); got != "default" {
		t.Errorf("default_model = %q, want the plain launch's pick left unsaved", got)
	}
}

// --model always wins over the saved default.
func TestModelFlagOverridesDefaultModel(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "flag-model")
	setPick(t, false)
	ask := askNobody

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
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
	ask := askWith(true, "picked-model", nil)

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
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
	ask := askWith(true, "fresh-choice", nil)

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
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
	ask := askWith(true, "fresh-choice", nil)

	if err := pickDefaultModel(cfg, ask); err != nil {
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
	ask := askWith(true, "chosen-model", nil)

	stderr := captureStream(t, &os.Stderr, func() {
		if err := pickDefaultModel(cfg, ask); err != nil {
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
	ask := askWith(true, "chosen-model", nil)

	stdout := captureStream(t, &os.Stdout, func() {
		if err := pickDefaultModel(cfg, ask); err != nil {
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

// A machine with no config file, such as one that signed in with a device key,
// gets one when --pick saves a default.
func TestPickDefaultModelCreatesTheConfig(t *testing.T) {
	useStubSwitch(t)
	home := emptyHome(t)
	setPick(t, true)

	if err := pickDefaultModel(nil, askWith(true, "picked-model", nil)); err != nil {
		t.Fatalf("pickDefaultModel: %v", err)
	}
	if got := readDefaultModel(t, home); got != "picked-model" {
		t.Fatalf("default_model = %q, want picked-model", got)
	}
}

// The picker's own mode gates the credential before the fetch, like --list
// does, so a keyless remote run names the missing key rather than reporting a
// network failure the operator reads as a rejected key.
func TestPickDefaultModelGatesBeforeTheFetch(t *testing.T) {
	envconfig.SetBaseURL("https://switch.example.test")
	envconfig.SetAPIKey("")
	t.Setenv(envconfig.KeyEnvVar, "")
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})

	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	ask := askWith(true, "picked", nil)

	err := pickDefaultModel(cfg, ask)
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
	ask := askWith(true, "picked", nil)

	if err := pickDefaultModel(cfg, ask); err == nil {
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
	ask := askNobody

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "flag-model" {
		t.Fatalf("resolved %q, want flag-model", chosen)
	}
}

// With no model, no default, and no terminal, the launch stops with an error
// that lists the names the key routes. A scripted run gets its choice from the
// same text `prizmal --list` prints, without opening a menu nobody can answer.
func TestNoModelAndNoTerminalFailsNamingTheRoutedModels(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, false)
	ask := askNobody

	_, _, err := resolveLaunchModel(cfg, "", ask, false)
	if err == nil {
		t.Fatal("a launch with no model and no terminal must fail")
	}
	for _, want := range []string{"--model", "--pick", "default_model", "saved-model", "picked-model", "flag-model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
	if got := readDefaultModel(t, home); got != "" {
		t.Errorf("default_model = %q, want a failed launch to save nothing", got)
	}
}

// A harness one-shot run is headless even when a terminal is attached: the
// command prints its answer and exits, so there is no session to pick a model
// for. The menu must not open, and the launch stops with the routed names.
func TestHeadlessRunDoesNotOpenTheMenu(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, false)
	// The menu records that it was reached, so the test fails on a menu that
	// opens whether or not it would have returned a choice or an error.
	menuOpened := false
	ask := launcher.Asking{
		Interactive: func() bool { return true },
		Menu: func([]launcher.ModelRow) (launcher.Selection, error) {
			menuOpened = true
			return launcher.Selection{Model: "picked-model"}, nil
		},
	}

	_, _, err := resolveLaunchModel(cfg, "", ask, true)
	if menuOpened {
		t.Fatal("the headless launch opened the menu")
	}
	if err == nil {
		t.Fatal("a headless launch with no model must fail rather than pick")
	}
	for _, want := range []string{"saved-model", "picked-model", "flag-model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name the routed model %q: %v", want, err)
		}
	}
	if got := readDefaultModel(t, home); got != "" {
		t.Errorf("default_model = %q, want the headless run to save nothing", got)
	}
}

// --pick is the operator asking for the menu. On a terminal it still opens
// for a headless run, because nothing else can answer for it.
func TestPickStillOpensOnATerminalForAHeadlessRun(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, true)
	ask := askWith(true, "picked-model", nil)

	chosen, _, err := resolveLaunchModel(cfg, "", ask, true)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want the pick", chosen)
	}
	if got := readDefaultModel(t, home); got != "picked-model" {
		t.Errorf("default_model = %q, want the pick saved", got)
	}
}

// A saved default is the operator's earlier choice, so a headless run uses it.
func TestSavedDefaultIsUsedInAHeadlessRun(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	setModel(t, "")
	setPick(t, false)

	chosen, _, err := resolveLaunchModel(cfg, "", askNobody, true)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "saved-model" {
		t.Fatalf("resolved %q, want saved-model", chosen)
	}
}

// The pick mode needs a terminal, and refuses without one rather than opening a
// menu nobody can answer.
func TestPickDefaultModelRefusesWithoutATerminal(t *testing.T) {
	useStubSwitch(t)
	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setPick(t, true)
	ask := askWith(false, "picked", nil)

	err := pickDefaultModel(cfg, ask)
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

	var sawRows []string
	ask := askWith(true, "chosen-here", &sawRows)

	chosen, catalog, err := resolveLaunchModel(cfg, "", ask, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "chosen-here" {
		t.Fatalf("resolved %q, want chosen-here", chosen)
	}
	// The stub's tenant holds no Claude tier, so the picker offers its three
	// models and no tier alias.
	if len(sawRows) != 3 {
		t.Fatalf("the picker was offered %d rows (%v), want the whole 3-model catalog and no tier", len(sawRows), sawRows)
	}
	if len(catalog) != 3 {
		t.Fatalf("catalog has %d models, want 3", len(catalog))
	}
	if got := readDefaultModel(t, home); got != "" {
		t.Fatalf("default_model = %q, want a plain launch's pick left unsaved", got)
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
			ask := askWith(true, "picked-model", nil)

			chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
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
	home := emptyHome(t)
	ask := askWith(true, "picked-model", nil)

	chosen, _, err := resolveLaunchModel(nil, "", ask, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel(nil, empty): %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want picked-model", chosen)
	}
	assertConfigFields(t, home, map[string]any{"version": float64(1), "default_model": "picked-model"})
}

// assertConfigFields fails unless the config file under home holds exactly want.
func assertConfigFields(t *testing.T, home string, want map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, config.PrizmalDir, config.PrizmalFileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %v, want %v", got, want)
	}
}

// A save with no loaded config keeps the fields of the file already on disk.
func TestSaveDefaultModelWithNilConfigKeepsExistingFields(t *testing.T) {
	_, home := writeConfig(t, map[string]any{
		"version":       1,
		"base_url":      "https://switch.example.test",
		"api_key":       "sk-invented",
		"default_model": "old-model",
	})

	if err := saveDefaultModel(nil, "picked-model"); err != nil {
		t.Fatalf("saveDefaultModel: %v", err)
	}
	assertConfigFields(t, home, map[string]any{
		"version":       float64(1),
		"base_url":      "https://switch.example.test",
		"api_key":       "sk-invented",
		"default_model": "picked-model",
	})
}

// On a machine with no config the save creates one holding the version and the
// default alone.
func TestSaveDefaultModelWithNilConfigCreatesMinimalFile(t *testing.T) {
	home := emptyHome(t)

	if err := saveDefaultModel(nil, "picked-model"); err != nil {
		t.Fatalf("saveDefaultModel: %v", err)
	}
	assertConfigFields(t, home, map[string]any{"version": float64(1), "default_model": "picked-model"})
}

// A config that cannot be read fails the save and stays as it was, instead of
// being replaced by a fresh one.
func TestSaveDefaultModelWithNilConfigFailsOnUnreadableConfig(t *testing.T) {
	home := emptyHome(t)
	path := filepath.Join(home, config.PrizmalDir, config.PrizmalFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const broken = `{"api_key": "sk-invented", `
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := saveDefaultModel(nil, "picked-model"); err == nil {
		t.Fatal("saveDefaultModel succeeded on an unparsable config")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != broken {
		t.Fatalf("config after failed save = %q, %v; want it untouched", got, err)
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
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})

	cfg, _ := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, true)
	ask := askWith(true, "picked-model", nil)

	_, _, err := resolveLaunchModel(cfg, "", ask, false)
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
	ask := askNobody

	chosen, catalog, err := resolveLaunchModel(cfg, "", ask, false)
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
	ask := askWith(true, "picked-model", nil)

	_, _, err := resolveLaunchModel(cfg, "", ask, false)
	if err == nil {
		t.Fatal("the picker must not open when the model list could not be read")
	}
	if !strings.Contains(err.Error(), "could not read models") {
		t.Errorf("error does not say the list could not be read: %v", err)
	}
}

// emptyHome points HOME at a directory with no config file and returns it.
func emptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// The picker's save shortcut keeps the pick as the default, though the launch
// is a plain one.
func TestSaveShortcutSavesAPlainLaunchPick(t *testing.T) {
	useStubSwitch(t)
	cfg, home := writeConfig(t, map[string]any{"version": 1})
	setModel(t, "")
	setPick(t, false)

	chosen, _, err := resolveLaunchModel(cfg, "", askSaving("picked-model"), false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "picked-model" {
		t.Fatalf("resolved %q, want picked-model", chosen)
	}
	if got := readDefaultModel(t, home); got != "picked-model" {
		t.Fatalf("default_model = %q, want the shortcut to save the pick", got)
	}
}

// A pick taken in a plain launch runs that launch and is never saved, with or
// without a config file to hold it.
func TestPlainLaunchPickIsNeverSaved(t *testing.T) {
	useStubSwitch(t)
	setModel(t, "")
	setPick(t, false)

	cfg, home := writeConfig(t, map[string]any{"version": 1})
	if _, _, err := resolveLaunchModel(cfg, "", askWith(true, "picked-model", nil), false); err != nil {
		t.Fatalf("resolveLaunchModel with a config: %v", err)
	}
	if got := readDefaultModel(t, home); got != "" {
		t.Errorf("default_model = %q, want nothing saved", got)
	}

	home = emptyHome(t)
	if _, _, err := resolveLaunchModel(nil, "", askWith(true, "picked-model", nil), false); err != nil {
		t.Fatalf("resolveLaunchModel without a config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, config.PrizmalDir, config.PrizmalFileName)); !os.IsNotExist(err) {
		t.Errorf("a plain launch's pick created a config file (stat error: %v)", err)
	}
}

// --model picks the model for one launch and never writes, even when the
// operator also passes --pick's shortcut elsewhere or no config exists.
func TestModelFlagNeverWritesTheConfig(t *testing.T) {
	useStubSwitch(t)
	setPick(t, false)

	home := emptyHome(t)
	setModel(t, "flag-model")
	chosen, _, err := resolveLaunchModel(nil, "", askNobody, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "flag-model" {
		t.Fatalf("resolved %q, want flag-model", chosen)
	}
	if _, err := os.Stat(filepath.Join(home, config.PrizmalDir, config.PrizmalFileName)); !os.IsNotExist(err) {
		t.Errorf("--model created a config file (stat error: %v)", err)
	}

	cfg, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	if _, _, err := resolveLaunchModel(cfg, "", askNobody, false); err != nil {
		t.Fatalf("resolveLaunchModel with a config: %v", err)
	}
	if got := readDefaultModel(t, home); got != "saved-model" {
		t.Errorf("default_model = %q, want --model to leave the saved default alone", got)
	}
}

// On a device-login machine the config file does not exist. Saving a default
// creates it for the non-credential fields: the file carries the version and
// the default, and neither base_url nor api_key.
func TestSavedDefaultCreatesAConfigWithoutCredentials(t *testing.T) {
	useStubSwitch(t)
	home := emptyHome(t)
	setModel(t, "")
	setPick(t, false)

	cfg, err := ensureConfigWith(func(string) (string, error) { return "", nil }, false)
	if err != nil || cfg != nil {
		t.Fatalf("ensureConfigWith = %v, %v; want a nil config and no error", cfg, err)
	}
	if _, _, err := resolveLaunchModel(cfg, "", askSaving("picked-model"), false); err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, config.PrizmalDir, config.PrizmalFileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(fields) != 2 || fields["version"] != float64(1) || fields["default_model"] != "picked-model" {
		t.Fatalf("config = %s, want only version and default_model", data)
	}
}

// A bare launch on a device-login machine that saved a default earlier runs it
// without opening the picker.
func TestBareLaunchUsesTheSavedDefaultOnADeviceMachine(t *testing.T) {
	useStubSwitch(t)
	_, home := writeConfig(t, map[string]any{"version": 1, "default_model": "saved-model"})
	if err := os.WriteFile(filepath.Join(home, config.PrizmalDir, "device.key"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	setModel(t, "")
	setPick(t, false)

	cfg, err := ensureConfigWith(func(string) (string, error) { return "", nil }, false)
	if err != nil || cfg == nil {
		t.Fatalf("ensureConfigWith = %v, %v; want the saved config", cfg, err)
	}
	opened := false
	ask := askWith(true, "other-model", nil)
	menu := ask.Menu
	ask.Menu = func(rows []launcher.ModelRow) (launcher.Selection, error) {
		opened = true
		return menu(rows)
	}

	chosen, _, err := resolveLaunchModel(cfg, "", ask, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "saved-model" || opened {
		t.Fatalf("resolved %q (picker opened: %v), want saved-model without the picker", chosen, opened)
	}
}
