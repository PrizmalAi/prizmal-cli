package update

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// A process that PRIZMAL_ENV=testing marks can pose as any install method, so
// the terminal baselines draw every screen without a real brew or go install.
// The seam is inert outside that mode.
const (
	envTesting     = "PRIZMAL_ENV"
	testInstallEnv = "PRIZMAL_TEST_INSTALL"
	testURLEnv     = "PRIZMAL_TEST_UPDATE_URL"
)

func underTest() bool { return os.Getenv(envTesting) == "testing" }

// Current classifies the running binary. injected is the -X main.version value.
func Current(injected string) Install {
	exe := ResolveExecutable("")
	if underTest() {
		if method := os.Getenv(testInstallEnv); method != "" {
			return posing(method, injected, exe)
		}
	}
	bi, _ := debug.ReadBuildInfo()
	return Detect(injected, bi, exe)
}

// posing builds the install a testing process asked for. A homebrew pose
// treats the binary's grandparent directory as the Homebrew prefix, so
// <prefix>/bin/prizmal is both the link and the file an upgrade replaces.
func posing(method, injected, exe string) Install {
	inst := Install{Version: normalizeTag(injected), Exe: exe}
	switch method {
	case "homebrew":
		inst.Method, inst.BrewPrefix = Homebrew, filepath.Dir(filepath.Dir(exe))
	case "go-install":
		inst.Method, inst.Package = GoInstall, PackagePath
	case "source":
		inst.Method = Source
	case "archive":
		inst.Method = Archive
	}
	return inst
}

// baseURL is def, or the testing override when a testing process names one.
func baseURL(def string) string {
	if v := strings.TrimRight(os.Getenv(testURLEnv), "/"); v != "" && underTest() {
		return v
	}
	return def
}
