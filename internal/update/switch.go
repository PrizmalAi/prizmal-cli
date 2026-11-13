package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Switch is the deployment the check asks which release this install may be
// offered. The Switch decides, because publishing a release must not announce
// it: a release reaches an install when the Switch says it may.
type Switch struct {
	// BaseURL is the Switch the CLI already talks to, as the config resolved
	// it. Empty means there is no Switch to ask, and the check reads the
	// release list.
	BaseURL string
	// Credential returns the switch key or device token the CLI holds, or ""
	// for none. The check calls it only when it is due to ask, so a check the
	// 24-hour cache answers never mints a device token. Nil means none.
	Credential func() string
}

// latestPath is the Switch route that reports the newest release the caller
// may be offered.
const latestPath = "/v1/cli/releases/latest"

// errSwitchUnanswered means the Switch gave no answer about releases: it has no
// such route, or it could not be reached.
var errSwitchUnanswered = errors.New("the Switch gave no release answer")

// askSwitch reads the newest release the Switch offers this caller.
//
// A 200 is the answer, and an empty version is an answer too: nothing is
// announced yet. A refused credential gets one retry with none, and the
// anonymous answer counts. It returns errSwitchUnanswered when the Switch has
// no such route (404, or a 401 that survives the retry, which is how an older
// Switch refuses a keyless request before it routes) or cannot be reached. Any
// other status means a Switch that has the route and cannot answer, and that is
// an error of its own.
func askSwitch(sw Switch) (string, error) {
	if switchBase(sw.BaseURL) == "" {
		return "", fmt.Errorf("%w: no Switch to ask", errSwitchUnanswered)
	}
	credential := ""
	if sw.Credential != nil {
		credential = strings.TrimSpace(sw.Credential())
	}
	version, status, err := getSwitchVersion(sw.BaseURL, credential)
	if err == nil && status == http.StatusUnauthorized && credential != "" {
		version, status, err = getSwitchVersion(sw.BaseURL, "")
	}
	switch {
	case err != nil && status == 0:
		return "", fmt.Errorf("%w: %v", errSwitchUnanswered, err)
	case err != nil:
		return "", err
	case status == http.StatusOK:
		return normalizeTag(version), nil
	case status == http.StatusNotFound, status == http.StatusUnauthorized:
		return "", fmt.Errorf("%w: HTTP %d", errSwitchUnanswered, status)
	default:
		return "", fmt.Errorf("release check: Switch answered HTTP %d", status)
	}
}

// getSwitchVersion sends one request. It returns the status with a nil error
// for every HTTP answer except a 200 whose body is not the expected JSON, which
// is an error with the status set.
func getSwitchVersion(base, credential string) (string, int, error) {
	req, err := http.NewRequest(http.MethodGet, switchBase(base)+latestPath, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "prizmal-cli")
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return "", resp.StatusCode, nil
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", resp.StatusCode, fmt.Errorf("release check: Switch answer is not JSON: %w", err)
	}
	return strings.TrimSpace(out.Version), resp.StatusCode, nil
}

// switchBase is the Switch to ask, without a trailing slash, or "" for none. A
// testing process asks a Switch on this machine and no other, so a test that
// names none cannot reach a real deployment.
func switchBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if underTest() && !loopbackURL(base) {
		return ""
	}
	return base
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
