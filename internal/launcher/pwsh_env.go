package launch

import (
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// defaultPwshEnvironmentSupported reports whether a PowerShell 7.3+
// -Environment-capable pwsh is available on Windows, via the shared pwsh
// version probe.
func defaultPwshEnvironmentSupported() bool {
	if pwshGOOS != "windows" {
		return false
	}
	return pwshWindowsSupportsEnvironment()
}

// startProcessCommand builds a Start-Process PowerShell command string. When
// env is non-nil and non-empty, it includes the -Environment hashtable with the
// given variables, deterministically ordered by key. When env is nil or empty,
// no -Environment block is emitted (the config-file fallback path). When
// useFilePath is true the target is passed via -FilePath; otherwise it is a
// positional argument (used for shell:AppsFolder\<AppID> launches).
func startProcessCommand(target string, useFilePath bool, env map[string]string) string {
	var b strings.Builder
	b.WriteString("Start-Process ")
	if useFilePath {
		b.WriteString("-FilePath ")
	}
	b.WriteString(quotePowerShellString(target))

	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString(" -Environment @{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(quotePowerShellString(k))
			b.WriteString(" = ")
			b.WriteString(quotePowerShellString(env[k]))
		}
		b.WriteString("}")
	}
	return b.String()
}

// pwshGOOS is the OS seam for the pwsh probe, overridable in tests.
var pwshGOOS = runtime.GOOS

// pwshEnvMinMajor and pwshEnvMinMinor are the minimum pwsh version whose
// Start-Process accepts -Environment (added in PowerShell 7.3).
const pwshEnvMinMajor, pwshEnvMinMinor = 7, 3

// pwshVersion is indirected so tests can override the pwsh version probe.
var pwshVersion = pwshProbeVersion

// pwshProbeVersion reports the installed pwsh (major, minor) version, or
// (0, 0) when unavailable.
func pwshProbeVersion() (int, int) {
	if pwshGOOS != "windows" {
		return 0, 0
	}
	out, err := exec.Command("pwsh", "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()").Output()
	if err != nil {
		return 0, 0
	}
	return pwshParseVersion(strings.TrimSpace(string(out)))
}

// pwshParseVersion parses an "X.Y[.Z]" version string.
func pwshParseVersion(version string) (int, int) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0
	}
	return major, minor
}

// pwshWindowsSupportsEnvironment reports whether the installed pwsh is
// 7.3+ and therefore supports Start-Process -Environment.
func pwshWindowsSupportsEnvironment() bool {
	major, minor := pwshVersion()
	return pwshWindowsSupportsWith(major, minor)
}

// pwshWindowsSupportsWith is the pure version comparison used by
// pwshWindowsSupportsEnvironment, split out so tests can exercise it
// without stubbing the probe seam.
func pwshWindowsSupportsWith(major, minor int) bool {
	if major != pwshEnvMinMajor {
		return major > pwshEnvMinMajor
	}
	return minor >= pwshEnvMinMinor
}
