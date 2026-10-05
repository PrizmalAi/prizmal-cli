// Package envconfig resolves the provider base URL that launched harnesses
// are pointed at: from --url or $PRIZMAL_SWITCH_URL, else the PrizmalSwitch
// production API.
package envconfig

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// EnvVar is the environment variable consulted for the provider URL.
	EnvVar = "PRIZMAL_SWITCH_URL"

	// KeyEnvVar is the environment variable for the provider API key.
	KeyEnvVar = "PRIZMAL_SWITCH_KEY"

	// DefaultURL is used when neither the flag, the environment, nor the
	// config file is set. It points at the PrizmalSwitch production API.
	DefaultURL = "https://api.prizmal.ai"
)

var (
	mu          sync.RWMutex
	override    string
	configURL   string
	keyOverride string
	configKey   string
	deviceToken string
	deviceMode  bool
)

// KeySource names where the provider API key came from. The names are printed
// verbatim on stderr, and they carry no key material.
type KeySource string

const (
	// KeySourceFlag means --api-key set the key.
	KeySourceFlag KeySource = "--api-key"
	// KeySourceEnv means $PRIZMAL_SWITCH_KEY set the key.
	KeySourceEnv KeySource = "$" + KeyEnvVar
	// KeySourceConfig means the config file's api_key set the key.
	KeySourceConfig KeySource = "config file (~/.prizmal/config.json)"
	// KeySourceDevice means an enrolled device produced the credential: the
	// CLI refreshes a device token from ~/.prizmal/device.key and hands it to
	// the launch instead of a switch key.
	KeySourceDevice KeySource = "device login (~/.prizmal/device.key)"
	// KeySourceNone means no source produced a key. The wording stays neutral
	// between an unauthenticated launch and an unauthenticated listing, which
	// share this announcement.
	KeySourceNone KeySource = "none (unauthenticated)"
)

// SetDeviceMode marks this process as running from an enrolled device. In
// device mode the credential is a device token (a signed, short-lived bearer),
// not a switch key, and the launchers that cannot refresh one refuse to run.
func SetDeviceMode(on bool) {
	mu.Lock()
	defer mu.Unlock()
	deviceMode = on
}

// DeviceMode reports whether this process runs in device mode.
func DeviceMode() bool {
	mu.RLock()
	defer mu.RUnlock()
	return deviceMode
}

// SetDeviceToken sets the device token a device-mode process holds. It is used
// for the catalog fetch, which authenticates with the same bearer the harness
// will send.
func SetDeviceToken(tok string) {
	mu.Lock()
	defer mu.Unlock()
	deviceToken = strings.TrimSpace(tok)
}

// SetConfigBaseURL records the provider URL read from the config file. It sits
// below the --url flag and $PRIZMAL_SWITCH_URL in precedence.
func SetConfigBaseURL(u string) {
	mu.Lock()
	defer mu.Unlock()
	configURL = strings.TrimRight(strings.TrimSpace(u), "/")
}

// SetAPIKey sets the process-wide provider API key (used by the --api-key flag).
func SetAPIKey(k string) {
	mu.Lock()
	defer mu.Unlock()
	keyOverride = strings.TrimSpace(k)
}

// SetConfigAPIKey records the resolved api_key from the config file. It sits
// below the --api-key flag and $PRIZMAL_SWITCH_KEY in precedence, the same
// place the config file's base_url sits for the URL.
func SetConfigAPIKey(k string) {
	mu.Lock()
	defer mu.Unlock()
	configKey = strings.TrimSpace(k)
}

// APIKey returns the configured provider API key: flag override, then
// $PRIZMAL_SWITCH_KEY, then the config file's api_key, then empty
// (unauthenticated).
func APIKey() string {
	k, _ := resolveAPIKey()
	return k
}

// APIKeySource names the source APIKey resolves from, never the key itself.
func APIKeySource() KeySource {
	_, source := resolveAPIKey()
	return source
}

func resolveAPIKey() (string, KeySource) {
	mu.RLock()
	flag, cfg, devTok, dev := keyOverride, configKey, deviceToken, deviceMode
	mu.RUnlock()
	// Device mode outranks every switch-key source. An enrolled device is the
	// credential the tenant approved; a stale $PRIZMAL_SWITCH_KEY exported in
	// the shell must not silently take its place and route the session under a
	// key the operator forgot about.
	if dev {
		if devTok != "" {
			return devTok, KeySourceDevice
		}
		return "", KeySourceNone
	}
	if flag != "" {
		return flag, KeySourceFlag
	}
	if v := EnvAPIKey(); v != "" {
		return v, KeySourceEnv
	}
	if cfg != "" {
		return cfg, KeySourceConfig
	}
	return "", KeySourceNone
}

// EnvAPIKey returns $PRIZMAL_SWITCH_KEY, trimmed.
func EnvAPIKey() string {
	return strings.TrimSpace(os.Getenv(KeyEnvVar))
}

// SetBaseURL sets the process-wide provider base URL (used by the --url flag).
func SetBaseURL(u string) {
	mu.Lock()
	defer mu.Unlock()
	override = strings.TrimRight(strings.TrimSpace(u), "/")
}

// BaseURL returns the configured provider base URL in precedence order:
// flag override, then $PRIZMAL_SWITCH_URL, then config-file base_url,
// then DefaultURL.
func BaseURL() string {
	mu.RLock()
	o := override
	cfg := configURL
	mu.RUnlock()
	if o != "" {
		return o
	}
	if v := strings.TrimSpace(os.Getenv(EnvVar)); v != "" {
		return strings.TrimRight(v, "/")
	}
	if cfg != "" {
		return cfg
	}
	return DefaultURL
}

// Host returns the provider base URL as a *url.URL. It never returns an
// error; an unparseable value falls back to DefaultURL.
func Host() *url.URL {
	u, err := url.Parse(BaseURL())
	if err != nil || u.Scheme == "" || u.Host == "" {
		u, _ = url.Parse(DefaultURL)
	}
	return u
}

// ConnectableHost returns the base URL rewritten so loopback addresses use
// "localhost".
func ConnectableHost() *url.URL {
	u := Host()
	if u.Hostname() == "127.0.0.1" || u.Hostname() == "0.0.0.0" {
		clone := *u
		clone.Host = "localhost" + ":" + u.Port()
		return &clone
	}
	return u
}

// ContextLength returns $HARNESS_CONTEXT_LENGTH (or 0 when unset).
func ContextLength() int {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HARNESS_CONTEXT_LENGTH")))
	return n
}
