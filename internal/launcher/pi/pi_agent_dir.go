package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// piAgentDirEnv names the variable Pi reads its agent directory from in place
// of ~/.pi/agent: models.json, settings.json, auth.json, sessions and the rest
// of the state Pi keeps. A launch points it at a directory of its own, so every
// write Pi makes back — a /model switch, a setting, a trust decision — lands
// there and goes with the launch's temporary directory.
//
// Pi expands a leading "~" in the value and takes it as it stands otherwise, so
// the launch sets an absolute path.
const piAgentDirEnv = "PI_CODING_AGENT_DIR"

// piSettingsName and piModelsName are the files the launch does not link. Pi
// rewrites settings.json for a /model switch, so the launch gives it a copy of
// its own, byte for byte. models.json is copied too, with the prizmal provider
// taken out: Pi validates that provider's rows while the extension registers,
// and an entry that is not complete — the one an older build wrote, or a
// hand-written model list — has no api for Pi to compose. The launch supplies
// the provider itself, and the user's file is not touched either way.
const (
	piSettingsName = "settings.json"
	piModelsName   = "models.json"
)

// piSessionsName is the directory Pi appends session transcripts to. It is
// user state, not configuration, so a launch keeps it in the user's directory:
// a session started through prizmal shows up in a later bare pi's /resume.
const piSessionsName = "sessions"

// piAgentDirPresent are the entries a launch links into the user's agent
// directory when the user has not created them yet: Pi writes them on first use
// through a link that already exists, so the trust decision a /trust makes, a
// credential refreshed into auth.json, and a refreshed provider catalog all land
// in the user's files rather than dying with the launch. A file linked before
// it exists is a dangling symlink, which Pi reads as absent and creates through
// the link.
var piAgentDirPresent = []string{"auth.json", "models-store.json", "trust.json"}

// piUserAgentDir is the agent directory the user's own pi runs with: the value
// of the variable when they set one, otherwise ~/.pi/agent. The launch links
// this directory's entries and never writes into it.
func piUserAgentDir() string {
	if dir := strings.TrimSpace(os.Getenv(piAgentDirEnv)); dir != "" {
		if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), string(filepath.Separator)))
			}
		}
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// writePiAgentDir builds the agent directory one launch points Pi at, under the
// launch's own directory, and returns its path.
//
// Every top-level entry of the user's agent directory is linked in, except the
// two configuration files, which are copied. Linking the rest keeps what a user
// expects to keep — their extensions, skills, prompts, themes, their sessions —
// through the same paths Pi always reads, while the files a launch must not let
// Pi rewrite are the launch's own.
//
// An entry Pi creates during a session, one the user's directory does not hold
// at launch time, is written into the launch directory and goes with it. The
// entries under piAgentDirPresent are linked ahead of time so the writes a
// session is most likely to make land in the user's directory instead.
func writePiAgentDir(overlayDir, userDir, model string) (string, error) {
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		return "", err
	}

	if userDir == "" {
		// A process with no home directory has no user configuration to keep
		// out of: the launch still gets an agent directory of its own, and
		// everything Pi writes stays in it.
		return overlayDir, nil
	}

	entries, err := os.ReadDir(userDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == piSettingsName || name == piModelsName {
			continue
		}
		target := filepath.Join(userDir, name)
		if err := linkPiEntry(target, filepath.Join(overlayDir, name), entry.IsDir()); err != nil {
			return "", fmt.Errorf("link %s: %w", target, err)
		}
	}

	if err := linkPiSessionsDir(overlayDir, userDir); err != nil {
		return "", err
	}
	for _, name := range piAgentDirPresent {
		link := filepath.Join(overlayDir, name)
		if _, err := os.Lstat(link); err == nil {
			// The user's directory already holds it, and the walk above linked
			// it.
			continue
		}
		// Best effort: a link to a file that does not exist yet needs the
		// symlink privilege on Windows, and without it Pi writes these files
		// into the launch directory instead. A launch must not fail over a
		// file neither pi nor the launch has created yet.
		_ = linkPiAbsentEntry(filepath.Join(userDir, name), link)
	}

	if err := copyPiSettings(overlayDir, userDir, model); err != nil {
		return "", err
	}
	if err := copyPiModels(overlayDir, userDir); err != nil {
		return "", err
	}
	return overlayDir, nil
}

// linkPiSessionsDir links Pi's sessions directory into the launch, creating it
// in the user's directory first when a first run has not. A session that ran to
// the end of the launch would otherwise be deleted with it, and the operator
// would have no transcript of what the launch did.
func linkPiSessionsDir(overlayDir, userDir string) error {
	link := filepath.Join(overlayDir, piSessionsName)
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	userSessions := filepath.Join(userDir, piSessionsName)
	if err := os.MkdirAll(userSessions, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", userSessions, err)
	}
	if err := linkPiEntry(userSessions, link, true); err != nil {
		return fmt.Errorf("link %s: %w", userSessions, err)
	}
	return nil
}

// copyPiModels gives the launch its own models.json: the user's bytes with the
// prizmal provider removed.
//
// The provider the launch uses is registered by the extension, and Pi composes
// a provider it finds in models.json with the extension's, validating the file's
// rows on the way. An entry an older build wrote names $PRIZMAL_CLIENT_VERSION
// and carries the live key on a legacy install, and a hand-written entry may
// name no api at all; either can fail that composition and take the whole
// launch down. Leaving it out of the copy keeps the launch on the provider the
// extension registers, whatever the user's file holds.
//
// A file that cannot be parsed is copied as it is. Pi reports its parse error
// the way it always has, and a launch does not turn a broken file into a
// different broken file.
func copyPiModels(overlayDir, userDir string) error {
	data, err := os.ReadFile(filepath.Join(userDir, piModelsName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	config := make(map[string]any)
	if json.Unmarshal(data, &config) == nil {
		if providers, ok := config["providers"].(map[string]any); ok {
			if _, present := providers[piProviderID]; present {
				delete(providers, piProviderID)
				config["providers"] = providers
				if stripped, err := json.MarshalIndent(config, "", "  "); err == nil {
					return os.WriteFile(filepath.Join(overlayDir, piModelsName), stripped, 0o600)
				}
			}
		}
	}
	return os.WriteFile(filepath.Join(overlayDir, piModelsName), data, 0o600)
}

// copyPiSettings gives the launch its own settings.json: the user's settings
// with the launch's provider and model named as the default.
//
// The default is what Pi's picker marks, and what a session that switches away
// from the launch's model returns to, so the launch names its own in the copy.
// Pi persists a /model switch into this file, and the file is the launch's: the
// user's settings.json keeps whatever default it had.
//
// A settings file that cannot be parsed is copied byte for byte and left for Pi
// to report, the same as models.json.
func copyPiSettings(overlayDir, userDir, model string) error {
	data, err := os.ReadFile(filepath.Join(userDir, piSettingsName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	settings := make(map[string]any)
	if len(data) > 0 && json.Unmarshal(data, &settings) != nil {
		return os.WriteFile(filepath.Join(overlayDir, piSettingsName), data, 0o600)
	}
	settings["defaultProvider"] = piProviderID
	if model != "" {
		settings["defaultModel"] = model
	}
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(overlayDir, piSettingsName), updated, 0o600)
}
