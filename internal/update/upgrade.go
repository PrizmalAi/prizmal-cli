package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// RepoPath is the repository as GitHub's URLs spell it: ModulePath without its
// host.
const RepoPath = "PrizmalAi/prizmal-cli"

// TapPath is the Homebrew tap that carries the prizmal cask.
const TapPath = "PrizmalAi/homebrew-tap"

// The hosts the check reads. They are variables so a test points them at a
// local server.
var (
	apiBase = "https://api.github.com"
	rawBase = "https://raw.githubusercontent.com"
)

// httpClient bounds one version query. A launch never waits longer than this
// for the network.
var httpClient = &http.Client{Timeout: 2 * time.Second}

func get(url string, accept string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "prizmal-cli")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// latestTag returns the newest GitHub release tag. A repository with no
// release answers "" and no error.
func latestTag() (string, error) {
	body, status, err := get(baseURL(apiBase)+"/repos/"+RepoPath+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("release check: HTTP %d", status)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.TagName, nil
}

var caskVersion = regexp.MustCompile(`(?m)^\s*version\s+"([^"]+)"`)

// latestCask returns the version the tap's cask installs. A brew user gets
// the cask, not the GitHub release, so the offer follows what brew can
// deliver: a release whose cask is not published yet is not offered.
func latestCask() (string, error) {
	body, status, err := get(baseURL(rawBase)+"/"+TapPath+"/main/Casks/"+CaskName+".rb", "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("cask check: HTTP %d", status)
	}
	m := caskVersion.FindSubmatch(body)
	if m == nil {
		return "", errors.New("cask check: no version line")
	}
	return normalizeTag(string(m[1])), nil
}

// LatestFor is the version query that matches how inst was installed.
func LatestFor(inst Install) func() (string, error) {
	if inst.Method == Homebrew {
		return latestCask
	}
	return latestTag
}

var tagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// ValidTag reports whether v is a v-prefixed semver tag. The upgrade command
// carries a tag from the network, so nothing else may reach it.
func ValidTag(v string) bool { return tagPattern.MatchString(v) }

// ErrNotSemver is the error IsNewer returns for a version it cannot read.
var ErrNotSemver = errors.New("not a semver tag")

// IsNewer reports whether latest is above current. Either side may omit the
// leading v. A prerelease sorts below its release, and build metadata after a
// plus sign is ignored. A version that does not parse is an error.
func IsNewer(current, latest string) (bool, error) {
	c, err := parseSemver(current)
	if err != nil {
		return false, err
	}
	l, err := parseSemver(latest)
	if err != nil {
		return false, err
	}
	return c.compare(l) < 0, nil
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(v string) (semver, error) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	var s semver
	var rest string
	if n, err := fmt.Sscanf(core+"|", "%d.%d.%d%s", &s.major, &s.minor, &s.patch, &rest); n < 3 || rest != "|" {
		_ = err
		return semver{}, fmt.Errorf("%w: %q", ErrNotSemver, v)
	}
	s.pre = pre
	return s, nil
}

func (s semver) compare(o semver) int {
	for _, p := range [][2]int{{s.major, o.major}, {s.minor, o.minor}, {s.patch, o.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case s.pre == o.pre:
		return 0
	case s.pre == "":
		return 1
	case o.pre == "":
		return -1
	}
	return strings.Compare(s.pre, o.pre)
}

// UpgradeCommand is the command that replaces inst with latest, as a person
// would type it. It is nil for an install nothing here can drive.
func (i Install) UpgradeCommand(latest string) []string {
	switch i.Method {
	case Homebrew:
		return []string{"brew", "upgrade", "--cask", CaskName}
	case GoInstall:
		return []string{"go", "install", i.Package + "@" + latest}
	}
	return nil
}

// Upgrade runs the upgrade command with its output on out and errOut. The tag
// is checked first, so nothing from the network reaches an exec.
func Upgrade(i Install, latest string, out, errOut io.Writer) error {
	if !ValidTag(latest) {
		return fmt.Errorf("refusing to upgrade to %q: not a release tag", latest)
	}
	argv := i.UpgradeCommand(latest)
	if argv == nil {
		return fmt.Errorf("no upgrade command for a %s install", i.Method)
	}
	if i.Method == Homebrew {
		// Use the brew that owns this install, not whichever is first on PATH.
		if brew := filepath.Join(i.BrewPrefix, "bin", "brew"); fileExists(brew) {
			argv[0] = brew
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, errOut
	cmd.Env = upgradeEnv(os.Environ())
	return cmd.Run()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// upgradeEnv is the environment brew and go run in. They are third-party
// builds, so the switch key stays out of it.
func upgradeEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, envconfig.KeyEnvVar+"=") {
			out = append(out, kv)
		}
	}
	return append(out, "HOMEBREW_NO_ENV_HINTS=1")
}
