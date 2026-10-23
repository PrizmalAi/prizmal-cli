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
	relayURL    string
	relayKey    string
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
	// KeySourceRelay means the launched harness holds the relay's per-launch
	// placeholder. The relay substitutes the real credential upstream.
	KeySourceRelay KeySource = "loopback relay placeholder"
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

// SetRelay enables the process-wide loopback relay override. While it is set,
// BaseURL returns baseURL and APIKey returns placeholder ahead of every other
// source. The values live only in memory.
func SetRelay(baseURL, placeholder string) {
	mu.Lock()
	defer mu.Unlock()
	relayURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	relayKey = strings.TrimSpace(placeholder)
}

// ClearRelay removes the loopback overrides and restores the ordinary URL and
// credential precedence.
func ClearRelay() {
	mu.Lock()
	defer mu.Unlock()
	relayURL = ""
	relayKey = ""
}

// RelayMode reports whether harness-facing URL and key reads use the relay.
func RelayMode() bool {
	mu.RLock()
	defer mu.RUnlock()
	return relayURL != ""
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

// APIKey returns the configured provider API key. A loopback relay placeholder
// outranks every other source; otherwise device mode, the flag override,
// $PRIZMAL_SWITCH_KEY, and the config file follow in that order.
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
	relay, relayActive := relayKey, relayURL != ""
	mu.RUnlock()
	if relayActive {
		return relay, KeySourceRelay
	}
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

// BaseURL returns the configured provider base URL. A loopback relay URL
// outranks every other source; otherwise the flag override,
// $PRIZMAL_SWITCH_URL, config-file base_url, and DefaultURL follow.
func BaseURL() string {
	return baseURL(true)
}

// UpstreamBaseURL returns the direct Switch base the relay forwards to,
// ignoring the relay override but keeping every other URL source unchanged.
func UpstreamBaseURL() string {
	return baseURL(false)
}

func baseURL(useRelay bool) string {
	mu.RLock()
	o := override
	cfg := configURL
	relay := relayURL
	mu.RUnlock()
	if useRelay && relay != "" {
		return relay
	}
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
