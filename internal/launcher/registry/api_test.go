package registry

import (
	"slices"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// wantAPIs is the wire APIs each harness speaks to the Switch. A harness that
// reads one API only is in the table with that one, so a request for another is
// refused instead of ignored.
var wantAPIs = map[string][]string{
	"claude":   {"anthropic"},
	"codex":    {"responses"},
	"cline":    {"chat"},
	"pi":       {"chat"},
	"opencode": {"chat", "responses"},
	"kilo":     {"chat", "responses", "anthropic"},
}

// TestSelectAPIAcceptsOnlyWhatTheHarnessSpeaks runs every harness against every
// API name: the ones it speaks are accepted, the others are refused with an
// error that names the harness and the APIs it does support.
func TestSelectAPIAcceptsOnlyWhatTheHarnessSpeaks(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		want, ok := wantAPIs[spec.Name]
		if !ok {
			t.Errorf("integration %q has no entry in wantAPIs", spec.Name)
			continue
		}
		reader, ok := spec.Runner.(launch.APIRunner)
		if !ok {
			t.Errorf("%s does not say which APIs it speaks", spec.Name)
			continue
		}
		if got := reader.SupportedAPIs(); !slices.Equal(got, want) {
			t.Errorf("%s SupportedAPIs = %v, want %v", spec.Name, got, want)
		}
		for _, api := range []string{"chat", "responses", "anthropic"} {
			err := launch.SelectAPI(spec.Name, spec.Runner, api)
			if slices.Contains(want, api) {
				if err != nil {
					t.Errorf("%s --api %s = %v, want it accepted", spec.Name, api, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("%s --api %s was accepted, want a refusal", spec.Name, api)
				continue
			}
			for _, part := range []string{spec.Name, "--api " + api, strings.Join(want, ", ")} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("%s --api %s error %q does not name %q", spec.Name, api, err, part)
				}
			}
		}
	}
}

func TestSelectAPIEmptyKeepsTheDefaultAndUnknownIsRefused(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		if err := launch.SelectAPI(spec.Name, spec.Runner, ""); err != nil {
			t.Errorf("%s with no --api = %v", spec.Name, err)
		}
		if err := launch.SelectAPI(spec.Name, spec.Runner, "bogus"); err == nil || !strings.Contains(err.Error(), "chat, responses, anthropic") {
			t.Errorf("%s --api bogus = %v, want an error naming the three APIs", spec.Name, err)
		}
	}
}
