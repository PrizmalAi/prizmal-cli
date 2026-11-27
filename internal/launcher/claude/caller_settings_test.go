package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// launchSettingsJSONForTest is the settings a device-mode launch builds: the
// model, the remaps, the picker rows and the apiKeyHelper. A launch cannot
// authenticate without the helper and cannot route without the rest, so these
// are the keys a caller's own --settings must never replace.
func launchSettingsJSONForTest(t *testing.T) string {
	t.Helper()
	settings, err := claudeSettingsJSON("smart", []launch.ModelRow{{Label: "smart", Model: "smart"}})
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	device, err := deviceSettingsJSON(settings)
	if err != nil {
		t.Fatalf("deviceSettingsJSON: %v", err)
	}
	return device
}

// launchArgsForTest drives the seam a launch uses, and removes anything it
// wrote when the test ends.
func launchArgsForTest(t *testing.T, own string, extra []string) []string {
	t.Helper()
	args, cleanup, err := (&Claude{}).launchArgs("smart", nil, own, extra)
	if err != nil {
		t.Fatalf("launchArgs: %v", err)
	}
	t.Cleanup(cleanup)
	return args
}

// settingsValues returns every --settings value Claude Code would read, which
// stops at the first -- because everything after it is positional.
func settingsValues(t *testing.T, args []string) []string {
	t.Helper()
	var values []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if value, ok := strings.CutPrefix(args[i], "--settings="); ok {
			values = append(values, value)
			continue
		}
		if args[i] != "--settings" {
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("--settings has no value in %v", args)
		}
		values = append(values, args[i+1])
		i++
	}
	return values
}

// settingsJSONFrom reads the settings the launch passes: inline JSON, or the
// file the argument names.
func settingsJSONFrom(t *testing.T, value string) string {
	t.Helper()
	if strings.HasPrefix(strings.TrimSpace(value), "{") {
		return value
	}
	data, err := os.ReadFile(value)
	if err != nil {
		t.Fatalf("the --settings value %q is neither JSON nor a readable file: %v", value, err)
	}
	return string(data)
}

func decodeSettings(t *testing.T, data string) map[string]any {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(data), &settings); err != nil {
		t.Fatalf("settings is not a JSON object: %v\n%s", err, data)
	}
	return settings
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

// writeSettingsFile writes a caller's settings file and returns its path.
func writeSettingsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caller-settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write the caller's settings: %v", err)
	}
	return path
}

// Claude Code keeps only one --settings, and the caller's argument replaced the
// launch's own: the session lost its model, its picker rows and the
// apiKeyHelper it authenticates with, and every turn failed with 401 Invalid
// API key. A launch now hands Claude Code exactly one merged --settings.
func TestClaudeLaunchPassesOneSettingsArgument(t *testing.T) {
	own := launchSettingsJSONForTest(t)
	const callerJSON = `{"theme":"dark","permissions":{"allow":["Bash(ls)"]}}`
	path := writeSettingsFile(t, callerJSON)

	for _, tc := range []struct {
		name     string
		extra    []string
		caller   bool // the caller's keys must arrive in the merged settings
		position bool // the --settings spelling is a positional argument
	}{
		{name: "an attached file argument", extra: []string{"--settings=" + path, "--verbose"}, caller: true},
		{name: "a separate file argument", extra: []string{"--settings", path, "--verbose"}, caller: true},
		{name: "an inline JSON argument", extra: []string{"--settings", callerJSON, "--verbose"}, caller: true},
		{name: "an attached inline JSON argument", extra: []string{"--settings=" + callerJSON, "--verbose"}, caller: true},
		{name: "no caller settings", extra: []string{"--verbose"}},
		{name: "a caller settings argument after --", extra: []string{"--", "--settings=" + path}, position: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := launchArgsForTest(t, own, tc.extra)

			// A positional argument after -- is the prompt, not a flag: the
			// harness must still receive it untouched.
			if tc.position {
				positional := args[len(args)-len(tc.extra):]
				if strings.Join(positional, " ") != strings.Join(tc.extra, " ") {
					t.Fatalf("the arguments after -- were rewritten: %v", args)
				}
			} else {
				// The caller's own spellings are consumed: only the merged
				// value carries them now.
				for _, arg := range tc.extra {
					if arg == path || arg == callerJSON {
						if containsArg(args, arg) {
							t.Fatalf("the caller's %q survived as its own argument: %v", arg, args)
						}
					}
				}
			}

			values := settingsValues(t, args)
			if len(values) != 1 {
				t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
			}
			settings := settingsJSONFrom(t, values[0])
			if !tc.caller {
				if settings != own {
					t.Fatalf("the launch's settings changed with no caller settings:\n got: %s\nwant: %s", settings, own)
				}
				return
			}

			got := decodeSettings(t, settings)
			if got["theme"] != "dark" {
				t.Errorf("the caller's theme was dropped: %v", got["theme"])
			}
			permissions, _ := got["permissions"].(map[string]any)
			if permissions == nil {
				t.Errorf("the caller's permissions block was dropped: %v", got)
			}
			if got["model"] == nil || got["modelPicker"] == nil || got["apiKeyHelper"] == nil {
				t.Errorf("the launch's own settings were dropped: %v", got)
			}
		})
	}
}

// A caller's settings file may hold a credential, and a command line is
// readable by any process that can list them. The merged object therefore
// travels in a private file that is removed when the launch ends, never in an
// argument.
func TestClaudeLaunchKeepsCallerSettingsOffTheCommandLine(t *testing.T) {
	const secret = "caller-token-that-must-not-be-an-argument"
	path := writeSettingsFile(t, `{"env":{"CALLER_TOKEN":"`+secret+`"}}`)

	args, cleanup, err := (&Claude{}).launchArgs("smart", nil, launchSettingsJSONForTest(t), []string{"--settings", path})
	if err != nil {
		t.Fatalf("launchArgs: %v", err)
	}
	for _, arg := range args {
		if strings.Contains(arg, secret) {
			t.Fatalf("the caller's token reached the command line in %q", arg)
		}
	}

	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	settingsPath := values[0]
	if strings.HasPrefix(strings.TrimSpace(settingsPath), "{") {
		t.Fatalf("the merged settings are inline in argv: %q", settingsPath)
	}
	if base := filepath.Base(settingsPath); !strings.HasPrefix(base, "prizmal-claude-settings-") {
		t.Errorf("the merged settings file is called %q, want a prizmal-claude-settings name", base)
	}
	if !strings.HasPrefix(settingsPath, os.TempDir()) {
		t.Errorf("the merged settings live in %q, want the temporary directory", settingsPath)
	}
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat the merged settings: %v", err)
	}
	// Windows has no POSIX mode: Stat reports a synthetic 0666, and access is
	// decided by the ACL of the per-user temporary directory the file lives
	// in. Everywhere else the file must be owner-only.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("the merged settings are mode %#o, want 0600", perm)
		}
	}
	if data, err := os.ReadFile(settingsPath); err != nil || !strings.Contains(string(data), secret) {
		t.Fatalf("the merged settings do not carry the caller's key (err %v):\n%s", err, data)
	}

	cleanup()
	if _, err := os.Stat(settingsPath); err == nil {
		t.Errorf("the launch left %s behind after it ended", settingsPath)
	}
}

// A key both sides set keeps the launch's value. Without the apiKeyHelper and
// the model settings the launch cannot authenticate and cannot route, so the
// caller's value only ever adds keys the launch does not state.
func TestClaudeLaunchSettingsMergeKeepsTheLaunchsOwnKeys(t *testing.T) {
	own := launchSettingsJSONForTest(t)
	ownSettings := decodeSettings(t, own)
	path := writeSettingsFile(t, `{"model":"caller-model","apiKeyHelper":"echo caller-key","modelPicker":{"replaceBuiltInOptions":false,"options":[]},"statusLine":{"type":"command","command":"echo hi"}}`)

	args := launchArgsForTest(t, own, []string{"--settings=" + path})
	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	got := decodeSettings(t, settingsJSONFrom(t, values[0]))

	if got["model"] != ownSettings["model"] {
		t.Errorf("model = %v, want the launch's %v", got["model"], ownSettings["model"])
	}
	if got["apiKeyHelper"] != ownSettings["apiKeyHelper"] {
		t.Errorf("apiKeyHelper = %v, want the launch's %v", got["apiKeyHelper"], ownSettings["apiKeyHelper"])
	}
	picker, _ := got["modelPicker"].(map[string]any)
	if picker == nil || picker["replaceBuiltInOptions"] != true {
		t.Errorf("modelPicker = %v, want the launch's rows to stay authoritative", got["modelPicker"])
	}
	if got["statusLine"] == nil {
		t.Errorf("the caller's unrelated key was dropped: %v", got)
	}
}

// Two caller settings arguments merge in the order they appear, so the later
// one wins over the earlier one, the way a stack of settings files does.
func TestClaudeLaunchSettingsMergeReadsCallerSettingsInOrder(t *testing.T) {
	first := writeSettingsFile(t, `{"theme":"dark","first":true}`)

	args := launchArgsForTest(t, launchSettingsJSONForTest(t), []string{"--settings", first, "--settings", `{"theme":"light","second":true}`})
	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	got := decodeSettings(t, settingsJSONFrom(t, values[0]))
	if got["theme"] != "light" {
		t.Errorf("theme = %v, want the later argument's %q", got["theme"], "light")
	}
	if got["first"] != true || got["second"] != true {
		t.Errorf("a caller key was dropped: %v", got)
	}
}

// An empty object is an object: it merges like any other, adding nothing and
// taking nothing away. Read as an absent value instead, a later caller argument
// stating {} would silently erase what an earlier one put under that key.
func TestClaudeLaunchSettingsMergeTreatsAnEmptyObjectAsAnObject(t *testing.T) {
	args := launchArgsForTest(t, launchSettingsJSONForTest(t), []string{
		"--settings", `{"env":{"CALLER":"1"}}`,
		"--settings", `{"env":{},"permissions":{}}`,
	})
	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	got := decodeSettings(t, settingsJSONFrom(t, values[0]))
	env, _ := got["env"].(map[string]any)
	if env["CALLER"] != "1" {
		t.Errorf("env = %v, want the earlier argument's CALLER key kept", env)
	}
	if got["model"] == nil || got["apiKeyHelper"] == nil {
		t.Errorf("the launch's own keys were dropped: %v", got)
	}
}

// A caller settings path that cannot be read fails the launch with the path in
// the message, rather than handing Claude Code an argument it cannot parse.
func TestClaudeLaunchSettingsMergeReportsAnUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	_, _, err := (&Claude{}).launchArgs("smart", nil, launchSettingsJSONForTest(t), []string{"--settings", missing})
	if err == nil {
		t.Fatal("a missing settings file did not fail the launch")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the file %q", err, missing)
	}

	if _, _, err := (&Claude{}).launchArgs("smart", nil, "", []string{"--settings", "not json"}); err == nil {
		t.Error("an unreadable settings argument did not fail the launch")
	}
}

// The deep merge keeps a caller's nested key beside the launch's values under
// the same object, so a settings block the caller extends is not replaced
// whole.
func TestClaudeLaunchSettingsMergeExtendsNestedObjects(t *testing.T) {
	own := `{"model":"m[1m]","env":{"PRIZMAL_OWN":"1"},"modelPicker":{"replaceBuiltInOptions":true}}`
	args := launchArgsForTest(t, own, []string{"--settings", `{"env":{"CALLER":"1"},"modelPicker":{"replaceBuiltInOptions":false}}`})
	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	got := decodeSettings(t, settingsJSONFrom(t, values[0]))
	env, _ := got["env"].(map[string]any)
	if env["PRIZMAL_OWN"] != "1" || env["CALLER"] != "1" {
		t.Errorf("env = %v, want the launch's and the caller's keys", env)
	}
	picker, _ := got["modelPicker"].(map[string]any)
	if picker["replaceBuiltInOptions"] != true {
		t.Errorf("modelPicker = %v, want the launch's replaceBuiltInOptions", picker)
	}
}

// A launch with no caller settings writes nothing: its own settings hold no
// credential, so they stay on the command line.
func TestClaudeLaunchWithoutCallerSettingsStaysOnTheCommandLine(t *testing.T) {
	args := launchArgsForTest(t, launchSettingsJSONForTest(t), []string{"--verbose"})
	values := settingsValues(t, args)
	if len(values) != 1 {
		t.Fatalf("got %d --settings arguments, want exactly 1: %v", len(values), args)
	}
	if !strings.HasPrefix(strings.TrimSpace(values[0]), "{") {
		t.Fatalf("the launch's own settings left the command line for %q", values[0])
	}
}
