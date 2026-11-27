package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// settingsField is one key of a settings object. The value stays raw JSON so a
// nested object keeps its own key order through the merge: Claude Code reads
// modelOverrides in order, and re-encoding a decoded map would sort it (see
// orderedModelOverrides).
type settingsField struct {
	key   string
	value json.RawMessage
}

// settingsObject is a settings object with its key order kept.
type settingsObject []settingsField

// mergedSettings is the one settings object a launch hands Claude Code.
type mergedSettings struct {
	// json is the object, empty when the launch has no settings at all.
	json string
	// caller reports that a caller's settings are part of the object. Their
	// content came from a file the caller keeps, which may hold a credential,
	// so a launch passes such an object through a private file instead of its
	// command line.
	caller bool
}

// decodeSettingsObject reads one JSON object into an ordered settingsObject.
// Blank input is a nil object, because a launch with no model builds no
// settings at all; {} is an empty object like any other.
func decodeSettingsObject(data []byte) (settingsObject, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("settings JSON is not an object")
	}
	object := make(settingsObject, 0)
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("settings JSON has a key that is not a string")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		object = append(object, settingsField{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("settings JSON has more than one value")
	}
	return object, nil
}

// encodeSettingsObject writes a settingsObject back out with its key order
// kept. Values are the raw JSON they were decoded from, so nothing inside them
// is reordered or reformatted either.
func encodeSettingsObject(object settingsObject) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, field := range object {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(field.value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// overlaySettings merges over onto base and returns the result with base's key
// order first. A key both sides state keeps its first position, and takes the
// winner's value: base's when overWins is false, over's when it is true. A key
// only over states is appended in over's order. Two objects under the same key
// merge recursively, so a block one side extends keeps the other's inside it.
func overlaySettings(base, over settingsObject, overWins bool) settingsObject {
	merged := make(settingsObject, len(base), len(base)+len(over))
	copy(merged, base)
	position := make(map[string]int, len(base))
	for i, field := range base {
		position[field.key] = i
	}
	for _, field := range over {
		if i, ok := position[field.key]; ok {
			merged[i].value = overlayValues(merged[i].value, field.value, overWins)
			continue
		}
		position[field.key] = len(merged)
		merged = append(merged, field)
	}
	return merged
}

// overlayValues is overlaySettings for two JSON values: objects merge, and
// anything else, arrays included, takes the winner's value whole.
func overlayValues(base, over json.RawMessage, overWins bool) json.RawMessage {
	baseObject, baseErr := decodeSettingsObject(base)
	overObject, overErr := decodeSettingsObject(over)
	if baseErr != nil || overErr != nil || baseObject == nil || overObject == nil {
		if overWins {
			return over
		}
		return base
	}
	data, err := encodeSettingsObject(overlaySettings(baseObject, overObject, overWins))
	if err != nil {
		if overWins {
			return over
		}
		return base
	}
	return json.RawMessage(data)
}

// readCallerSettings reads one --settings value the caller wrote: an inline
// JSON object, or a path to a settings file. Claude Code accepts both
// spellings, and a launch must not guess which one the caller meant, so the
// leading brace decides.
func readCallerSettings(value string) (settingsObject, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") {
		object, err := decodeSettingsObject([]byte(trimmed))
		if err != nil {
			return nil, fmt.Errorf("parse the caller's --settings JSON: %w", err)
		}
		return object, nil
	}
	data, err := os.ReadFile(value)
	if err != nil {
		return nil, fmt.Errorf("read the caller's --settings file: %w", err)
	}
	object, err := decodeSettingsObject(data)
	if err != nil {
		return nil, fmt.Errorf("parse the caller's --settings file %s: %w", value, err)
	}
	return object, nil
}

// mergeCallerSettings folds every --settings the caller typed into the
// launch's own settings JSON, and returns that one object with the arguments
// that remain for Claude Code.
//
// Claude Code keeps only one --settings, and a caller's argument used to
// replace the launch's inline JSON outright. The session then ran without the
// model, the picker rows or the apiKeyHelper, so every request failed with 401
// Invalid API key: the caller could only ever remove what makes the launch
// work. The launch's keys win a conflict, because without them it cannot
// authenticate or route, and the caller's other keys ride along in the same
// object. Caller arguments merge left to right first, so a later one wins over
// an earlier one the way a stack of settings files does.
//
// Arguments after -- are positional, so a --settings spelling there is part of
// the prompt and is left alone.
func mergeCallerSettings(settingsJSON string, extra []string) (mergedSettings, []string, error) {
	own, err := decodeSettingsObject([]byte(settingsJSON))
	if err != nil {
		return mergedSettings{}, nil, fmt.Errorf("parse the launch's Claude Code settings: %w", err)
	}
	var caller settingsObject
	fromCaller := false
	kept := make([]string, 0, len(extra))
	for i := 0; i < len(extra); i++ {
		arg := extra[i]
		if arg == "--" {
			kept = append(kept, extra[i:]...)
			break
		}
		value, attached := strings.CutPrefix(arg, "--settings=")
		if !attached {
			if arg != "--settings" {
				kept = append(kept, arg)
				continue
			}
			if i+1 >= len(extra) {
				return mergedSettings{}, nil, fmt.Errorf("--settings has no value")
			}
			i++
			value = extra[i]
		}
		next, err := readCallerSettings(value)
		if err != nil {
			return mergedSettings{}, nil, err
		}
		caller = overlaySettings(caller, next, true)
		fromCaller = true
	}
	if len(bytes.TrimSpace([]byte(settingsJSON))) == 0 && !fromCaller {
		return mergedSettings{}, kept, nil
	}
	data, err := encodeSettingsObject(overlaySettings(own, caller, false))
	if err != nil {
		return mergedSettings{}, nil, fmt.Errorf("merge the caller's Claude Code settings: %w", err)
	}
	return mergedSettings{json: string(data), caller: fromCaller}, kept, nil
}

// writeMergedSettings writes the merged settings to a file only its owner can
// read and returns the path.
//
// The file is os.CreateTemp's, so it lives in os.TempDir() (TMPDIR, or %TEMP%
// on Windows) under a prizmal-claude-settings- name, with mode 0600. The
// temporary directory is the per-user one, so the file is readable by its owner
// and nobody else; Windows has no POSIX mode and relies on the ACL of that
// directory, which keeps the file to its owner in the same way.
func writeMergedSettings(settings string) (string, error) {
	f, err := os.CreateTemp("", "prizmal-claude-settings-*.json")
	if err != nil {
		return "", fmt.Errorf("write the merged Claude Code settings: %w", err)
	}
	path := f.Name()
	_, writeErr := f.WriteString(settings)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if writeErr != nil {
			return "", fmt.Errorf("write the merged Claude Code settings: %w", writeErr)
		}
		return "", fmt.Errorf("write the merged Claude Code settings: %w", closeErr)
	}
	return path, nil
}

// launchArgs builds the command line for a launch, and returns the cleanup that
// removes anything it had to write.
//
// When a caller's settings are merged in, the object travels in a private
// temporary file named by the --settings argument rather than as the argument
// itself. The caller's file may hold a credential, and a command line is
// readable by any process that can list them, so inlining the file's contents
// would publish what the caller kept private. The file is
// os.TempDir()/prizmal-claude-settings-*.json, mode 0600, and the returned
// cleanup removes it. Run defers that cleanup around cmd.Run, so the file goes
// when Claude Code exits with a zero status, when it exits non-zero, and when
// the process never starts at all. What keeps it past that is a prizmal no
// program can finish: a SIGKILL or a power loss leaves it in the temporary
// directory, where it holds the caller's own settings and never a prizmal key.
//
// A launch with no caller settings writes no file and keeps its own object on
// the command line, where it was before: that object holds no credential,
// because the apiKeyHelper is a command rather than the token it prints.
func (c *Claude) launchArgs(model string, rows []launch.ModelRow, settingsJSON string, extra []string) ([]string, func(), error) {
	settings, extra, err := mergeCallerSettings(settingsJSON, extra)
	if err != nil {
		return nil, nil, err
	}
	value := settings.json
	cleanup := func() {}
	if settings.caller && value != "" {
		path, err := writeMergedSettings(value)
		if err != nil {
			return nil, nil, err
		}
		value = path
		cleanup = func() { _ = os.Remove(path) }
	}
	return c.args(model, rows, value, extra), cleanup, nil
}
