package opencode

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

func TestOpenCodeSendsTheClientVersion(t *testing.T) {
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })

	cfg := openCodeInlineConfig(t)
	provider, _ := cfg["provider"].(map[string]any)["prizmal"].(map[string]any)
	options, _ := provider["options"].(map[string]any)
	headers, _ := options["headers"].(map[string]any)
	if got := headers["x-prizmal-client-version"]; got != "1.4.2" {
		t.Errorf("options.headers = %v, want x-prizmal-client-version 1.4.2", headers)
	}
}
