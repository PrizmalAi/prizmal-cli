package internaltest

import (
	"os"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"regexp"
	"strings"
	"testing"
)

// EnvValue returns the value of the var named after "prefix=" in env, or "".
func EnvValue(env []string, prefix string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// SandboxedHome points HOME and USERPROFILE at a temp dir and returns it.
func SandboxedHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d) // windows
	return d
}

// credentialEnvVars are the variables a launch reads a credential from. A test
// that asserts on the absence of a key must not see the operator's own.
var credentialEnvVars = []string{
	"PRIZMAL_SWITCH_KEY",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
}

// ClearCredentialEnv empties every credential variable a launch reads for the
// duration of a test, so a key exported in the operator's shell cannot change
// the outcome or reach a failure message.
func ClearCredentialEnv(t *testing.T) {
	t.Helper()
	for _, name := range credentialEnvVars {
		t.Setenv(name, "")
	}
}

// UnsetSwitchURLEnv removes $PRIZMAL_SWITCH_URL for the duration of a test. An
// empty value is not enough where code asks whether the variable is present.
func UnsetSwitchURLEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envconfig.EnvVar, "") // registers the restore
	if err := os.Unsetenv(envconfig.EnvVar); err != nil {
		t.Fatalf("unset %s: %v", envconfig.EnvVar, err)
	}
}

var secretName = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|AUTH|CREDENTIAL)`)

// RedactEnv returns env with the value of every credential-looking variable
// replaced by "<redacted>". Use it for any env that goes into a failure
// message: a child environment inherits the operator's shell.
func RedactEnv(env []string) []string {
	out := make([]string, len(env))
	for i, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && secretName.MatchString(name) {
			out[i] = name + "=<redacted>"
			continue
		}
		out[i] = kv
	}
	return out
}

// RedactEnvDump is RedactEnv for a newline-separated NAME=value dump.
func RedactEnvDump(dump string) string {
	return strings.Join(RedactEnv(strings.Split(dump, "\n")), "\n")
}
