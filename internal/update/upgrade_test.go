package update

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want, wantErr   bool
	}{
		{"v0.1.2", "v0.1.3", true, false},
		{"v0.1.2", "v0.2.0", true, false},
		{"v0.9.9", "v1.0.0", true, false},
		{"v0.1.9", "v0.1.10", true, false},
		{"v0.1.2", "v0.1.2", false, false},
		{"v0.1.3", "v0.1.2", false, false},
		{"v0.1.2", "v0.1.2-rc1", false, false},
		{"v0.1.2-rc1", "v0.1.2", true, false},
		{"v0.1.3-0.20261005182724-84fe65cc8617", "v0.1.3", true, false},
		{"v0.1.3-0.20261005182724-84fe65cc8617", "v0.1.2", false, false},
		{"v0.1.2+dirty", "v0.1.2", false, false},
		{"v0.1.2+dirty", "v0.1.3", true, false},
		{"0.1.2", "v0.2.0", true, false},
		{"v0.1.2", "0.2.0", true, false},
		{"v0.1.2", "", false, true},
		{"", "v0.2.0", false, true},
		{"nightly", "v1.0.0", false, true},
		{"v1.2", "v1.3.0", false, true},
		{"v1.2.3.4", "v1.3.0", false, true},
	}
	for _, tc := range cases {
		got, err := IsNewer(tc.current, tc.latest)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, %v; want %v, error %v", tc.current, tc.latest, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, ok := range []string{"v0.1.2", "v10.20.30", "v0.1.3-rc1", "v0.1.3-0.20261005182724-84fe65cc8617"} {
		if !ValidTag(ok) {
			t.Errorf("ValidTag(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "0.1.2", "main", "v1", "v1.2", "v1.2.3; rm -rf /", "v1.2.3 --flag", "v1.2.3\n", "v1.2.3/../x", "$(id)", "v1.2.3+meta"} {
		if ValidTag(bad) {
			t.Errorf("ValidTag(%q) = true", bad)
		}
	}
}

func serve(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	oldAPI, oldRaw := apiBase, rawBase
	apiBase, rawBase = srv.URL, srv.URL
	t.Cleanup(func() { apiBase, rawBase = oldAPI, oldRaw })
}

func TestLatestTag(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{"release", 200, `{"tag_name":"v0.2.0"}`, "v0.2.0", false},
		{"no release yet", 404, `{}`, "", false},
		{"rate limited", 403, `{"message":"rate limit"}`, "", true},
		{"server error", 500, ``, "", true},
		{"not json", 200, `<html>`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path, agent string
			serve(t, func(w http.ResponseWriter, r *http.Request) {
				path, agent = r.URL.Path, r.Header.Get("User-Agent")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := latestTag()
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("latestTag = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
			if path != "/repos/"+RepoPath+"/releases/latest" {
				t.Errorf("queried %q", path)
			}
			if agent == "" {
				t.Error("no User-Agent: GitHub rejects the request")
			}
		})
	}
}

func TestLatestTagUnreachable(t *testing.T) {
	serve(t, func(http.ResponseWriter, *http.Request) {})
	apiBase = "http://127.0.0.1:1"
	if got, err := latestTag(); err == nil {
		t.Fatalf("latestTag = %q, nil; want an error", got)
	}
}

func TestLatestCask(t *testing.T) {
	const cask = "cask \"prizmal\" do\n  version \"0.9.9\"\n\n  name \"prizmal\"\nend\n"
	var path string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(cask))
	})
	got, err := latestCask()
	if err != nil || got != "v0.9.9" {
		t.Fatalf("latestCask = %q, %v; want v0.9.9", got, err)
	}
	if path != "/"+TapPath+"/main/Casks/prizmal.rb" {
		t.Errorf("queried %q", path)
	}
}

func TestLatestCaskFailures(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
	if _, err := latestCask(); err == nil {
		t.Error("a 404 cask read as a version")
	}
	serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("cask \"prizmal\" do\nend\n")) })
	if _, err := latestCask(); err == nil {
		t.Error("a cask with no version line read as a version")
	}
}

func TestLatestForFollowsTheInstallMethod(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			_, _ = w.Write([]byte(`{"tag_name":"v0.3.0"}`))
			return
		}
		_, _ = w.Write([]byte("  version \"0.2.0\"\n"))
	})
	for method, want := range map[Method]string{Homebrew: "v0.2.0", GoInstall: "v0.3.0", Archive: "v0.3.0", Source: "v0.3.0"} {
		got, err := LatestFor(Install{Method: method})()
		if err != nil || got != want {
			t.Errorf("%s: latest = %q, %v; want %q", method, got, err, want)
		}
	}
}

func TestUpgradeCommand(t *testing.T) {
	brew := Install{Method: Homebrew}.UpgradeCommand("v0.2.0")
	if strings.Join(brew, " ") != "brew upgrade --cask prizmal" {
		t.Errorf("brew = %v", brew)
	}
	goi := Install{Method: GoInstall, Package: PackagePath}.UpgradeCommand("v0.2.0")
	if strings.Join(goi, " ") != "go install github.com/PrizmalAi/prizmal-cli/cmd/prizmal@v0.2.0" {
		t.Errorf("go install = %v", goi)
	}
	for _, m := range []Method{Archive, Source, Unknown} {
		if got := (Install{Method: m}).UpgradeCommand("v0.2.0"); got != nil {
			t.Errorf("%s has an upgrade command: %v", m, got)
		}
	}
}

// fakeCommand writes an executable shell script that records its arguments and
// exits with code.
func fakeCommand(t *testing.T, path string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	record := filepath.Join(filepath.Dir(path), filepath.Base(path)+".args")
	script := "#!/bin/sh\necho \"$@\" > '" + record + "'\necho out-from-stub\necho err-from-stub >&2\nexit " + string(rune('0'+code)) + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestUpgradeRunsTheBrewThatOwnsTheInstall(t *testing.T) {
	prefix := t.TempDir()
	record := fakeCommand(t, filepath.Join(prefix, "bin", "brew"), 0)
	var out, errOut strings.Builder
	if err := Upgrade(Install{Method: Homebrew, BrewPrefix: prefix}, "v0.2.0", &out, &errOut); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	args, _ := os.ReadFile(record)
	if strings.TrimSpace(string(args)) != "upgrade --cask prizmal" {
		t.Errorf("brew ran with %q", args)
	}
	if !strings.Contains(out.String(), "out-from-stub") || !strings.Contains(errOut.String(), "err-from-stub") {
		t.Errorf("output was not passed through: %q / %q", out.String(), errOut.String())
	}
}

func TestUpgradeGoInstallPinsTheTag(t *testing.T) {
	dir := t.TempDir()
	record := fakeCommand(t, filepath.Join(dir, "go"), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Upgrade(Install{Method: GoInstall, Package: PackagePath}, "v0.2.0", new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	args, _ := os.ReadFile(record)
	if strings.TrimSpace(string(args)) != "install "+PackagePath+"@v0.2.0" {
		t.Errorf("go ran with %q", args)
	}
}

func TestUpgradeReportsAFailingCommand(t *testing.T) {
	prefix := t.TempDir()
	fakeCommand(t, filepath.Join(prefix, "bin", "brew"), 1)
	if err := Upgrade(Install{Method: Homebrew, BrewPrefix: prefix}, "v0.2.0", new(strings.Builder), new(strings.Builder)); err == nil {
		t.Fatal("a failing brew read as success")
	}
}

func TestUpgradeRefusesATagThatIsNotAReleaseTag(t *testing.T) {
	dir := t.TempDir()
	record := fakeCommand(t, filepath.Join(dir, "go"), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, bad := range []string{"", "main", "v1.2.3; touch pwned", "$(id)", "v1.2.3 --other"} {
		if err := Upgrade(Install{Method: GoInstall, Package: PackagePath}, bad, new(strings.Builder), new(strings.Builder)); err == nil {
			t.Errorf("Upgrade accepted %q", bad)
		}
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("a command ran for a bad tag")
	}
}

func TestUpgradeHasNothingToRunForAnArchive(t *testing.T) {
	if err := Upgrade(Install{Method: Archive}, "v0.2.0", new(strings.Builder), new(strings.Builder)); err == nil {
		t.Fatal("Upgrade of an archive install succeeded")
	}
}

func TestUpgradeKeepsTheSwitchKeyOutOfBrewAndGo(t *testing.T) {
	got := upgradeEnv([]string{"PATH=/bin", "PRIZMAL_SWITCH_KEY=pz-secret", "PRIZMAL_SWITCH_KEYS_EXTRA=kept", "HOME=/h"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "pz-secret") || strings.Contains(joined, "PRIZMAL_SWITCH_KEY=") {
		t.Fatalf("the switch key reached the upgrade environment: %v", got)
	}
	for _, want := range []string{"PATH=/bin", "HOME=/h", "PRIZMAL_SWITCH_KEYS_EXTRA=kept", "HOMEBREW_NO_ENV_HINTS=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("upgrade environment lost %q: %v", want, got)
		}
	}
}

func TestUpgradeRunsWithoutTheSwitchKey(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nenv > '" + filepath.Join(dir, "seen") + "'\n"
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRIZMAL_SWITCH_KEY", "pz-secret")
	if err := Upgrade(Install{Method: GoInstall, Package: PackagePath}, "v0.2.0", new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "seen"))
	if strings.Contains(string(seen), "pz-secret") {
		t.Fatal("the spawned go process saw the switch key")
	}
}
