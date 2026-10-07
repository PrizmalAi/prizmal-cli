package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(OffEnv, "")
}

func pinGoBin(t *testing.T, dir string) {
	t.Helper()
	old := goBinDir
	goBinDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { goBinDir = old })
}

var (
	now      = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	goInst   = Install{Method: GoInstall, Version: "v0.1.2", Exe: "/x/bin/prizmal", Package: PackagePath}
	brewInst = Install{Method: Homebrew, Version: "v0.1.2", Exe: "/b/Caskroom/prizmal/0.1.2/prizmal", BrewPrefix: "/b"}
	archInst = Install{Method: Archive, Version: "v0.1.2", Exe: "/usr/local/bin/prizmal"}
)

// counter is a version query that counts its calls.
type counter struct {
	tag   string
	err   error
	calls int
}

func (c *counter) latest() (string, error) { c.calls++; return c.tag, c.err }

func TestCheckOffersAnUpgradeToAnInstallThatCanRunIt(t *testing.T) {
	setHome(t, t.TempDir())
	pinGoBin(t, "/x/bin")
	state := stateStore{dir: t.TempDir()}
	cases := map[string]Install{"go install": goInst, "homebrew": brewInst}
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot replace a running binary")
	}
	for name, inst := range cases {
		o := check(now, inst, state, (&counter{tag: "v0.2.0"}).latest)
		if o.Action != Offer || o.Latest != "v0.2.0" || o.Install != inst {
			t.Errorf("%s: outcome = %+v, want Offer v0.2.0", name, o)
		}
		state = stateStore{dir: t.TempDir()}
	}
}

func TestCheckWarnsWhenTheInstallCannotUpgradeItself(t *testing.T) {
	setHome(t, t.TempDir())
	pinGoBin(t, "/elsewhere")
	src := Install{Method: Source, Version: "v0.1.3-0.20261005222209-a67f209a699e", Exe: "/src/prizmal"}
	for name, inst := range map[string]Install{"archive": archInst, "source": src, "go install outside GOBIN": goInst} {
		o := check(now, inst, stateStore{dir: t.TempDir()}, (&counter{tag: "v0.9.0"}).latest)
		if o.Action != Warn || o.Latest != "v0.9.0" {
			t.Errorf("%s: outcome = %+v, want Warn v0.9.0", name, o)
		}
	}
}

func TestCheckSaysNothingWhenCurrentOrAhead(t *testing.T) {
	setHome(t, t.TempDir())
	for _, tag := range []string{"v0.1.2", "v0.1.1", "v0.1.2-rc1"} {
		if o := check(now, archInst, stateStore{dir: t.TempDir()}, (&counter{tag: tag}).latest); o.Action != None {
			t.Errorf("latest %s: outcome = %+v, want None", tag, o)
		}
	}
}

func TestCheckHasNothingToCompareForAnUnclassifiedBinary(t *testing.T) {
	setHome(t, t.TempDir())
	q := &counter{tag: "v9.9.9"}
	for _, inst := range []Install{{}, {Method: Unknown, Version: "v0.1.2"}, {Method: Archive}} {
		if o := check(now, inst, stateStore{dir: t.TempDir()}, q.latest); o.Action != None {
			t.Errorf("%+v: outcome = %+v, want None", inst, o)
		}
	}
	if q.calls != 0 {
		t.Errorf("queried the network %d times for a binary with nothing to compare", q.calls)
	}
}

func TestCheckQueriesOnceADay(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	check(now, archInst, state, q.latest)
	o := check(now.Add(23*time.Hour), archInst, state, q.latest)
	if q.calls != 1 {
		t.Fatalf("23 hours later: %d queries, want 1", q.calls)
	}
	if o.Action != Warn {
		t.Errorf("the cached answer was not replayed: %+v", o)
	}
	check(now.Add(25*time.Hour), archInst, state, q.latest)
	if q.calls != 2 {
		t.Fatalf("25 hours later: %d queries, want 2", q.calls)
	}
}

func TestCheckQueriesAgainWhenTheBinaryChanges(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	check(now, archInst, state, q.latest)
	upgraded := archInst
	upgraded.Version = "v0.2.0"
	if o := check(now.Add(time.Hour), upgraded, state, q.latest); o.Action != None {
		t.Errorf("an upgraded binary was told to upgrade: %+v", o)
	}
	if q.calls != 2 {
		t.Errorf("%d queries, want a fresh one for the new version", q.calls)
	}
}

func TestCheckRetriesAFailedQueryAfterAnHour(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{err: os.ErrDeadlineExceeded}
	if o := check(now, archInst, state, q.latest); o.Action != None {
		t.Fatalf("a failed query answered %+v", o)
	}
	check(now.Add(30*time.Minute), archInst, state, q.latest)
	if q.calls != 1 {
		t.Fatalf("retried after 30 minutes: %d queries", q.calls)
	}
	q.err, q.tag = nil, "v0.2.0"
	if o := check(now.Add(61*time.Minute), archInst, state, q.latest); o.Action != Warn {
		t.Fatalf("after an hour: %+v, want Warn", o)
	}
	if q.calls != 2 {
		t.Fatalf("%d queries, want 2", q.calls)
	}
}

func TestCheckTreatsAnInvalidTagAsAFailedQuery(t *testing.T) {
	setHome(t, t.TempDir())
	for _, tag := range []string{"main", "v9.9.9; rm -rf /", ""} {
		if o := check(now, archInst, stateStore{dir: t.TempDir()}, (&counter{tag: tag}).latest); o.Action != None {
			t.Errorf("tag %q: outcome = %+v, want None", tag, o)
		}
	}
}

func TestSnoozeSilencesTheSameReleaseForADay(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	o := check(now, archInst, state, q.latest)
	if err := snooze(state, now, o); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	if o := check(now.Add(time.Hour), archInst, state, q.latest); o.Action != None {
		t.Errorf("an hour after answering: %+v, want None", o)
	}
	if o := check(now.Add(23*time.Hour), archInst, state, q.latest); o.Action != None {
		t.Errorf("23 hours after answering: %+v, want None", o)
	}
	if o := check(now.Add(25*time.Hour), archInst, state, q.latest); o.Action != Warn {
		t.Errorf("25 hours after answering: %+v, want Warn again", o)
	}
}

func TestSnoozeSurvivesTheDailyQuery(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	o := check(now, archInst, state, q.latest)
	_ = snooze(state, now.Add(23*time.Hour), o)
	// The query is due at 24h, and the answer at 23h still holds until 47h.
	if o := check(now.Add(30*time.Hour), archInst, state, q.latest); o.Action != None {
		t.Errorf("a refresh forgot the answer: %+v", o)
	}
}

func TestSnoozeDoesNotCoverANewerRelease(t *testing.T) {
	setHome(t, t.TempDir())
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	o := check(now, archInst, state, q.latest)
	_ = snooze(state, now, o)
	q.tag = "v0.3.0"
	if o := check(now.Add(25*time.Hour), archInst, state, q.latest); o.Action != Warn || o.Latest != "v0.3.0" {
		t.Errorf("a newer release stayed silent: %+v", o)
	}
}

func TestSnoozeStopsAnUpgradeThatChangedNothingFromAskingAgain(t *testing.T) {
	setHome(t, t.TempDir())
	pinGoBin(t, "/x/bin")
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot replace a running binary")
	}
	state := stateStore{dir: t.TempDir()}
	q := &counter{tag: "v0.2.0"}
	o := check(now, goInst, state, q.latest)
	if o.Action != Offer {
		t.Fatalf("outcome = %+v", o)
	}
	_ = snooze(state, now, o)
	// The upgrade "succeeded", the same binary relaunched, and it checks again.
	if o := check(now.Add(time.Minute), goInst, state, q.latest); o.Action != None {
		t.Errorf("the relaunched old binary asked again: %+v", o)
	}
}

func TestCheckHonoursTheOptOut(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv(OffEnv, "1")
	q := &counter{tag: "v0.2.0"}
	state := stateStore{dir: t.TempDir()}
	if o := check(now, archInst, state, q.latest); o.Action != None {
		t.Errorf("outcome = %+v with the check off", o)
	}
	if q.calls != 0 {
		t.Errorf("%d queries with the check off", q.calls)
	}
	if _, err := os.Stat(state.path()); err == nil {
		t.Error("the state file was written with the check off")
	}
}

func TestDisabled(t *testing.T) {
	writeConfig := func(t *testing.T, body string) {
		t.Helper()
		p, err := config.Path()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, env, config string
		want              bool
	}{
		{"on by default", "", "", false},
		{"env 1", "1", "", true},
		{"env true", "true", "", true},
		{"env 0 is on", "0", "", false},
		{"env false is on", "false", "", false},
		{"env off is on", "OFF", "", false},
		{"config false", "", `{"version":1,"check_updates":false}`, true},
		{"config true", "", `{"version":1,"check_updates":true}`, false},
		{"config absent field", "", `{"version":1,"base_url":"https://x"}`, false},
		{"config corrupt stays on", "", `{not json`, false},
		{"env overrides config true", "1", `{"version":1,"check_updates":true}`, true},
		{"env 0 overrides config false", "0", `{"version":1,"check_updates":false}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			t.Setenv(OffEnv, tc.env)
			if tc.config != "" {
				writeConfig(t, tc.config)
			}
			if got := Disabled(); got != tc.want {
				t.Fatalf("Disabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigKeepsCheckUpdatesAcrossASave(t *testing.T) {
	setHome(t, t.TempDir())
	off := false
	if err := (&config.Config{BaseURL: "https://x", CheckUpdates: &off}).Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil || loaded.CheckUpdates == nil || *loaded.CheckUpdates {
		t.Fatalf("Load = %+v, %v; want check_updates false", loaded, err)
	}
	loaded.DefaultModel = "smart" // what a pick does
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	if !Disabled() {
		t.Error("a later Save dropped check_updates")
	}
}

func TestRunCheckNeverWaitsPastItsDeadline(t *testing.T) {
	setHome(t, t.TempDir())
	slow := func() (string, error) { time.Sleep(5 * time.Second); return "v9.9.9", nil }
	start := time.Now()
	o := runCheck(now, archInst, stateStore{dir: t.TempDir()}, slow, 100*time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waited %s", d)
	}
	if o.Action != None {
		t.Fatalf("outcome = %+v, want None", o)
	}
}

func TestStateToleratesACorruptFileAndAnUnwritableDirectory(t *testing.T) {
	setHome(t, t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := &counter{tag: "v0.2.0"}
	if o := check(now, archInst, stateStore{dir: dir}, q.latest); o.Action != Warn {
		t.Errorf("corrupt state: %+v, want a fresh Warn", o)
	}
	// A state directory that cannot exist still answers.
	blocked := filepath.Join(dir, stateFile, "sub")
	if o := check(now, archInst, stateStore{dir: blocked}, q.latest); o.Action != Warn {
		t.Errorf("unwritable state: %+v, want Warn", o)
	}
	if o := check(now, archInst, stateStore{}, q.latest); o.Action != Warn {
		t.Errorf("no state directory: %+v, want Warn", o)
	}
}

func TestStateFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	state := stateStore{dir: filepath.Join(t.TempDir(), "d")}
	if err := state.write(cached{Current: "v0.1.2"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(state.path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestCurrentPosesOnlyForATestingProcess(t *testing.T) {
	t.Setenv(testInstallEnv, "homebrew")
	t.Setenv(envTesting, "")
	if got := Current("0.1.2"); got.Method == Homebrew && got.Version == "v0.1.2" {
		t.Fatal("the pose worked outside testing mode")
	}
	t.Setenv(envTesting, "testing")
	got := Current("0.1.2")
	if got.Method != Homebrew || got.Version != "v0.1.2" || got.BrewPrefix == "" {
		t.Fatalf("Current = %+v, want a v0.1.2 homebrew pose", got)
	}
	for method, want := range map[string]Method{"go-install": GoInstall, "source": Source, "archive": Archive} {
		t.Setenv(testInstallEnv, method)
		if got := Current("0.1.2"); got.Method != want {
			t.Errorf("%s pose = %v", method, got.Method)
		}
	}
}

func TestBaseURLOverrideNeedsTestingMode(t *testing.T) {
	t.Setenv(testURLEnv, "http://127.0.0.1:9/")
	t.Setenv(envTesting, "")
	if got := baseURL("https://api.github.com"); got != "https://api.github.com" {
		t.Fatalf("the override applied outside testing mode: %q", got)
	}
	t.Setenv(envTesting, "testing")
	if got := baseURL("https://api.github.com"); got != "http://127.0.0.1:9" {
		t.Fatalf("baseURL = %q", got)
	}
}
