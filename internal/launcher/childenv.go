package launch

import (
	"os"
	"strings"
)

// ChildEnv builds the environment a harness runs in: the inherited environment
// without the variables named in drop and without any variable fixed defines,
// followed by fixed (NAME=value pairs).
//
// The inherited pass skips every name the launch defines, so a variable never
// appears twice. A name defined twice would leave the child's value depending
// on which copy the OS reads first, and the launch's value must be the one that
// wins.
func ChildEnv(drop, fixed []string) []string {
	skip := make(map[string]bool, len(drop)+len(fixed))
	for _, name := range drop {
		skip[name] = true
	}
	for _, kv := range fixed {
		name, _, _ := strings.Cut(kv, "=")
		skip[name] = true
	}

	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+len(fixed))
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if skip[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, fixed...)
}
