package update

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
)

// OffEnv turns the check off when set to anything but 0, false, no or off.
// The config file's check_updates does the same.
const OffEnv = "PRIZMAL_NO_UPDATE_CHECK"

const (
	stateFile     = "update-check.json"
	checkInterval = 24 * time.Hour // between version queries, and between prompts
	retryInterval = time.Hour      // before a failed query runs again
	maxCheckDelay = 3 * time.Second
)

// Action is what a check asks the caller to do.
type Action int

const (
	// None: nothing to show.
	None Action = iota
	// Offer: a newer release exists and this install can replace itself.
	Offer
	// Warn: a newer release exists and this install cannot replace itself.
	Warn
)

// Outcome is one check's answer.
type Outcome struct {
	Action  Action
	Install Install
	Latest  string
}

type cached struct {
	Current    string `json:"current"`
	Checked    int64  `json:"checked"`
	Latest     string `json:"latest,omitempty"`
	Snoozed    int64  `json:"snoozed,omitempty"`
	SnoozedFor string `json:"snoozed_for,omitempty"`
}

func (c cached) due(now time.Time) bool {
	return !now.Before(time.Unix(c.Checked, 0).Add(checkInterval))
}

type stateStore struct{ dir string }

func (s stateStore) path() string { return filepath.Join(s.dir, stateFile) }

// read returns the cache, or a zero value when it is missing or unreadable.
func (s stateStore) read() cached {
	var c cached
	if data, err := os.ReadFile(s.path()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func (s stateStore) write(c cached) error {
	if s.dir == "" {
		return errors.New("no state directory")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), append(data, '\n'), 0o600)
}

// defaultState keeps the cache beside the config file.
func defaultState() stateStore {
	p, err := config.Path()
	if err != nil {
		return stateStore{}
	}
	return stateStore{dir: filepath.Dir(p)}
}

// Disabled reports whether the operator turned the check off, by the
// environment or by check_updates in the config file.
func Disabled() bool {
	if v := strings.ToLower(os.Getenv(OffEnv)); v != "" {
		return v != "0" && v != "false" && v != "no" && v != "off"
	}
	cfg, err := config.Load()
	return err == nil && cfg.CheckUpdates != nil && !*cfg.CheckUpdates
}

// CanUpgrade reports whether this install can run its own upgrade. Windows
// cannot replace a running binary, and a go install binary outside GOBIN would
// leave the old one running.
func (i Install) CanUpgrade() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	switch i.Method {
	case Homebrew:
		return true
	case GoInstall:
		bin, err := goBinDir()
		return err == nil && filepath.Dir(i.Exe) == bin
	}
	return false
}

// goBinDir is where `go install` writes: $GOBIN, else $GOPATH/bin.
var goBinDir = func() (string, error) {
	out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", errors.New("unexpected go env output")
	}
	if gobin := strings.TrimSpace(lines[0]); gobin != "" {
		return gobin, nil
	}
	if gopath := strings.TrimSpace(lines[1]); gopath != "" {
		return filepath.Join(gopath, "bin"), nil
	}
	return "", errors.New("go env has no GOBIN or GOPATH")
}

// RunCheck asks whether a newer release exists, at most once a day and for at
// most maxCheckDelay. Every failure answers None: the check never blocks or
// fails a launch.
func RunCheck(now time.Time, inst Install) Outcome {
	return runCheck(now, inst, defaultState(), LatestFor(inst), maxCheckDelay)
}

func runCheck(now time.Time, inst Install, state stateStore, latest func() (string, error), deadline time.Duration) Outcome {
	done := make(chan Outcome, 1)
	go func() { done <- check(now, inst, state, latest) }()
	select {
	case o := <-done:
		return o
	case <-time.After(deadline):
		return Outcome{}
	}
}

func check(now time.Time, inst Install, state stateStore, latest func() (string, error)) Outcome {
	if Disabled() || inst.Method == Unknown || inst.Version == "" {
		return Outcome{}
	}
	c := state.read()
	if c.Current != inst.Version || c.due(now) {
		fresh := cached{Current: inst.Version, Checked: now.Unix()}
		tag, err := latest()
		if err != nil || !ValidTag(tag) {
			// Retry in an hour, not tomorrow and not on every launch.
			fresh.Checked = now.Add(retryInterval - checkInterval).Unix()
		} else {
			fresh.Latest = tag
		}
		if c.Current == inst.Version && c.SnoozedFor == fresh.Latest {
			fresh.Snoozed, fresh.SnoozedFor = c.Snoozed, c.SnoozedFor
		}
		_ = state.write(fresh)
		c = fresh
	}
	return decide(now, inst, c)
}

func decide(now time.Time, inst Install, c cached) Outcome {
	if c.Latest == "" {
		return Outcome{}
	}
	if newer, err := IsNewer(inst.Version, c.Latest); err != nil || !newer {
		return Outcome{}
	}
	if c.SnoozedFor == c.Latest && now.Before(time.Unix(c.Snoozed, 0).Add(checkInterval)) {
		return Outcome{}
	}
	action := Warn
	if inst.CanUpgrade() {
		action = Offer
	}
	return Outcome{Action: action, Install: inst, Latest: c.Latest}
}

// Snooze records that the operator answered for o, so the next prompt for the
// same release waits a day. It also stops a relaunch after an upgrade that did
// not change the binary from asking again.
func Snooze(now time.Time, o Outcome) error { return snooze(defaultState(), now, o) }

func snooze(state stateStore, now time.Time, o Outcome) error {
	c := state.read()
	if c.Current != o.Install.Version {
		c = cached{Current: o.Install.Version, Checked: now.Unix(), Latest: o.Latest}
	}
	c.Snoozed, c.SnoozedFor = now.Unix(), o.Latest
	return state.write(c)
}
