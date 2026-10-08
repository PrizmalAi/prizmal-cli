package launch

import (
	"fmt"
	"slices"

	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
)

// RestoreOutcome says what a Restore did, so the caller can print a line
// that matches. A no-op on a machine prizmal never configured must not read
// like a removal, and a removal that put the user's previous settings back
// should say which.
type RestoreOutcome struct {
	// Removed is true when a launch configuration was found and taken out.
	Removed bool
	// Reinstated lists the pre-launch settings put back, as key=value, in
	// the order they were restored.
	Reinstated []string
}

// Join folds a second file's outcome into this one.
func (o RestoreOutcome) Join(other RestoreOutcome) RestoreOutcome {
	return RestoreOutcome{
		Removed:    o.Removed || other.Removed,
		Reinstated: slices.Concat(o.Reinstated, other.Reinstated),
	}
}

// PreLaunchCopy returns the newest backup of an integration's file whose
// content pointsAtPrizmal rejects: the copy fileutil.WriteWithBackup took
// before the first launch repointed the file. Later persists back up copies
// that already point at prizmal, and those are skipped. It is nil when no
// such copy survives, and Restore then falls back to clearing the pointers.
func PreLaunchCopy(integration, name string, pointsAtPrizmal func(map[string]any) bool) map[string]any {
	for _, path := range fileutil.Backups(integration, name) {
		previous, err := fileutil.ReadJSON(path)
		if err != nil || pointsAtPrizmal(previous) {
			continue
		}
		return previous
	}
	return nil
}

// reinstate puts the named keys of the pre-launch copy back into config. A
// key the copy holds is set; a key it lacks stays as the caller left it,
// which is deleted. It returns what it set, as key=value, for the outcome.
func Reinstate(config, previous map[string]any, keys ...string) []string {
	var put []string
	for _, key := range keys {
		value, ok := previous[key]
		if !ok {
			continue
		}
		config[key] = value
		put = append(put, fmt.Sprintf("%s=%v", key, value))
	}
	return put
}
