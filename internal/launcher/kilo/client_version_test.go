package kilo

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

func TestKiloSendsTheClientVersion(t *testing.T) {
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })

	config := inlineConfig(t, "smart", []launch.LaunchModel{{Name: "smart"}})
	provider := config["provider"].(map[string]any)["prizmal"].(map[string]any)
	options := provider["options"].(map[string]any)
	headers, _ := options["headers"].(map[string]any)
	if got := headers["x-prizmal-client-version"]; got != "1.4.2" {
		t.Errorf("options.headers = %v, want x-prizmal-client-version 1.4.2", headers)
	}
}
