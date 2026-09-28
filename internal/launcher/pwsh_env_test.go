package launch

import (
	"strings"
	"testing"
)

// TestPwshVersionSupportsEnvironment adapts the source-repo string-version
// table test to the live repo's (major, minor) probe seam: each case's version
// string is parsed with pwshParseVersion, then checked against
// pwshWindowsSupportsWith.
func TestPwshVersionSupportsEnvironment(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"7.3", true},
		{"7.3.1", true},
		{"7.4.0", true},
		{"8.0", true},
		{"10.0", true},
		{"7.2", false},
		{"7.2.7", false},
		{"7.0", false},
		{"6.2", false},
		{"5.1", false},
		{"", false},
		{"7", false},
		{"abc", false},
		{"7.x", false},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			major, minor := pwshParseVersion(c.version)
			if got := pwshWindowsSupportsWith(major, minor); got != c.want {
				t.Errorf("supportsEnvironment(%q) = %v, want %v", c.version, got, c.want)
			}
		})
	}
}

func TestStartProcessCommandWithEnvironment(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":  "sk-test-key",
		"OPENAI_BASE_URL": "https://api.prizmal.ai/v1/",
	}
	cmd := startProcessCommand("C:\\Program Files\\ChatGPT\\ChatGPT.exe", true, env)
	if !strings.Contains(cmd, "Start-Process -FilePath ") {
		t.Errorf("missing -FilePath: %s", cmd)
	}
	if !strings.Contains(cmd, "-Environment @{") {
		t.Errorf("missing -Environment block: %s", cmd)
	}
	for k, v := range env {
		needle := quotePowerShellString(k) + " = " + quotePowerShellString(v)
		if !strings.Contains(cmd, needle) {
			t.Errorf("env var %s not interpolated correctly: %s", k, cmd)
		}
	}
}

func TestStartProcessCommandWithoutEnvironment(t *testing.T) {
	cmd := startProcessCommand("C:\\path\\app.exe", true, nil)
	if strings.Contains(cmd, "-Environment") {
		t.Errorf("should not include -Environment when env is nil: %s", cmd)
	}
	if !strings.Contains(cmd, "Start-Process -FilePath ") {
		t.Errorf("missing -FilePath: %s", cmd)
	}
}

func TestStartProcessCommandAppIDPositional(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "k"}
	cmd := startProcessCommand(`shell:AppsFolder\com.openai.codex`, false, env)
	// AppID launches use a positional target, not -FilePath.
	if strings.Contains(cmd, "-FilePath") {
		t.Errorf("AppID launch should not use -FilePath: %s", cmd)
	}
	if !strings.Contains(cmd, "Start-Process ") {
		t.Errorf("missing Start-Process: %s", cmd)
	}
	if !strings.Contains(cmd, "-Environment @{") {
		t.Errorf("missing -Environment block: %s", cmd)
	}
}

func TestStartProcessCommandEscapesApostrophes(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY": "sk-it's-a-key",
	}
	cmd := startProcessCommand("app.exe", true, env)
	// Apostrophes in values must be doubled inside single-quoted strings.
	if !strings.Contains(cmd, "'sk-it''s-a-key'") {
		t.Errorf("apostrophe not escaped: %s", cmd)
	}
}

func TestStartProcessCommandDeterministicKeyOrder(t *testing.T) {
	env := map[string]string{
		"OPENAI_BASE_URL": "u",
		"OPENAI_API_KEY":  "k",
	}
	cmd := startProcessCommand("app.exe", true, env)
	// Keys must appear in sorted order regardless of map iteration order.
	first := strings.Index(cmd, quotePowerShellString("OPENAI_API_KEY"))
	second := strings.Index(cmd, quotePowerShellString("OPENAI_BASE_URL"))
	if first == -1 || second == -1 || first > second {
		t.Errorf("keys not in deterministic sorted order: %s", cmd)
	}
}

func TestDefaultPwshVersionDetectionNonWindowsReturnsFalse(t *testing.T) {
	restoreGOOS := pwshGOOS
	pwshGOOS = "darwin"
	t.Cleanup(func() { pwshGOOS = restoreGOOS })
	if defaultPwshEnvironmentSupported() {
		t.Errorf("non-Windows should report unsupported")
	}
}

func TestDefaultPwshVersionDetectionParsesOutput(t *testing.T) {
	restoreGOOS := pwshGOOS
	restoreVer := pwshVersion
	pwshGOOS = "windows"
	pwshVersion = func() (int, int) { return 7, 4 }
	t.Cleanup(func() {
		pwshGOOS = restoreGOOS
		pwshVersion = restoreVer
	})
	if !defaultPwshEnvironmentSupported() {
		t.Errorf("pwsh 7.4 on Windows should report supported")
	}
}

func TestDefaultPwshVersionDetectionMalformedReturnsFalse(t *testing.T) {
	restoreGOOS := pwshGOOS
	restoreVer := pwshVersion
	pwshGOOS = "windows"
	pwshVersion = func() (int, int) { return 0, 0 }
	t.Cleanup(func() {
		pwshGOOS = restoreGOOS
		pwshVersion = restoreVer
	})
	if defaultPwshEnvironmentSupported() {
		t.Errorf("malformed pwsh version should report unsupported")
	}
}

func TestDefaultPwshVersionDetectionOldReturnsFalse(t *testing.T) {
	restoreGOOS := pwshGOOS
	restoreVer := pwshVersion
	pwshGOOS = "windows"
	pwshVersion = func() (int, int) { return 5, 1 }
	t.Cleanup(func() {
		pwshGOOS = restoreGOOS
		pwshVersion = restoreVer
	})
	if defaultPwshEnvironmentSupported() {
		t.Errorf("pwsh 5.1 should report unsupported")
	}
}

func TestDefaultPwshVersionDetectionExecErrorReturnsFalse(t *testing.T) {
	restoreGOOS := pwshGOOS
	restoreVer := pwshVersion
	pwshGOOS = "windows"
	pwshVersion = func() (int, int) { return 0, 0 }
	t.Cleanup(func() {
		pwshGOOS = restoreGOOS
		pwshVersion = restoreVer
	})
	if defaultPwshEnvironmentSupported() {
		t.Errorf("pwsh probe failure should report unsupported")
	}
}
