package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/spf13/cobra"
)

// appBaseURL is the admin app that serves the /cli/authorize consent page. An
// explicit $PRIZMAL_APP_URL wins; otherwise it is derived from the Switch host,
// so a staging login stays on the staging app.
func appBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("PRIZMAL_APP_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if host := envconfig.Host().Hostname(); host == "api.staging.prizmal.ai" {
		return "https://app.staging.prizmal.ai"
	}
	return "https://app.prizmal.ai"
}

// deviceKeyExists reports whether this machine has an enrolled (or
// re-approvable) device key.
func deviceKeyExists() bool {
	dir, err := device.Dir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, device.KeyFileName))
	return err == nil
}

// enterDeviceMode turns an enrolled device key into the launch's credential:
// it refreshes a device token and puts it in envconfig, where the catalog
// fetch and the launchers read it. It is a no-op when no device key exists.
//
// A token that cannot be refreshed — the sign-in deadline passed, the device
// was revoked — is an error the launch stops on, because a launch with no
// usable credential would fail at the first turn with a message that blames
// the wrong thing.
func enterDeviceMode() error {
	if !deviceKeyExists() {
		return nil
	}
	key, err := device.LoadKey()
	if err != nil {
		return err
	}
	envconfig.SetDeviceMode(true)

	client := device.NewClient(envconfig.BaseURL())
	token, ct, err := device.HelperToken(client, key, time.Now(), loadCachedToken, time.Sleep)
	if err != nil {
		if errors.Is(err, device.ErrReauthRequired) {
			return fmt.Errorf("this device's sign-in expired; run prizmal login to re-approve it")
		}
		if errors.Is(err, device.ErrNotAuthorized) || errors.Is(err, device.ErrDeviceUnknown) {
			return fmt.Errorf("device not authorized; run prizmal login to re-approve this device")
		}
		return err
	}
	envconfig.SetDeviceToken(token)
	if ct != nil {
		_ = ct.Save()
	}
	return nil
}

// loadCachedToken reads the token cache for HelperToken, returning nil when
// there is none.
func loadCachedToken() *device.CachedToken {
	ct, err := device.LoadCachedToken()
	if err != nil {
		return nil
	}
	return ct
}

// cmdLogin is `prizmal login`: it enrolls this machine, or re-approves the
// device it already enrolled.
//
// Re-approval is deliberate. The device key is generated once; a login on a
// machine that has one reuses it, so the Config API updates the same device
// row the tenant already trusts and the device id does not change. Minting a
// new key here would silently orphan the old enrollment and leave the operator
// comparing a fingerprint against a device the browser page never showed.
func cmdLogin(cmd *cobra.Command, _ []string) error {
	flagURL, _ := cmd.Flags().GetString("url")
	flagKey, _ := cmd.Flags().GetString("api-key")
	applyURLPrecedence(flagURL, flagKey)

	key, created, err := device.LoadOrCreateKey()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSigning in with your browser...\n")
	if created {
		fmt.Fprintf(os.Stderr, "Generated a new device key at ~/.prizmal/%s\n", device.KeyFileName)
	} else {
		fmt.Fprintf(os.Stderr, "Re-approving the device key already on this machine.\n")
	}

	client := device.NewClient(envconfig.BaseURL())
	token, err := device.Login(client, key, device.LoginOptions{
		AuthorizeBaseURL: appBaseURL(),
		DeviceName:       deviceName(),
		Out:              os.Stderr,
	})
	if err != nil {
		return err
	}

	if err := (&device.CachedToken{Token: token.Value, Expiry: token.Expiry, ReauthBy: token.ReauthBy}).Save(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%sThis device is approved.%s\n", launcher.AnsiGreen, launcher.AnsiReset)
	if token.ReauthBy != nil {
		fmt.Fprintf(os.Stderr, "Re-approve before %s.\n", token.ReauthBy.Format(time.RFC3339))
	}
	return nil
}

// cmdAuthToken is `prizmal auth token`, the Claude Code apiKeyHelper. It prints
// one device token on stdout and nothing else: Claude Code captures stdout and
// rejects anything that is not a single printable-ASCII line within its size
// limit. Every diagnostic goes to stderr.
func cmdAuthToken(cmd *cobra.Command, _ []string) error {
	flagURL, _ := cmd.Flags().GetString("url")
	flagKey, _ := cmd.Flags().GetString("api-key")
	applyURLPrecedence(flagURL, flagKey)

	key, err := device.LoadKey()
	if err != nil {
		if errors.Is(err, device.ErrNoDeviceKey) {
			return fmt.Errorf("no device key on this machine; run prizmal login")
		}
		return err
	}

	client := device.NewClient(envconfig.BaseURL())
	token, ct, err := device.HelperToken(client, key, time.Now(), loadCachedToken, time.Sleep)
	if err != nil {
		// The one 401 whose fix differs: the developer signs in again, which
		// re-approves the same device key. Every other refusal is the same
		// "device not authorized" the switch sends, and this never guesses
		// which check failed.
		if errors.Is(err, device.ErrReauthRequired) {
			return fmt.Errorf("run prizmal login")
		}
		if errors.Is(err, device.ErrNotAuthorized) || errors.Is(err, device.ErrDeviceUnknown) {
			return fmt.Errorf("device not authorized; run prizmal login")
		}
		return err
	}
	if !device.ValidHelperOutput(token) {
		return fmt.Errorf("the device token is not printable ASCII within Claude Code's limit")
	}
	if ct != nil {
		_ = ct.Save()
	}
	// stdout carries the token and nothing else.
	_, _ = fmt.Fprintln(os.Stdout, token)
	return nil
}

// applyURLPrecedence settles the base URL and key the way the root command
// does, for a subcommand that does not go through the root's RunE: --url, then
// $PRIZMAL_SWITCH_URL, then the config file. It never prompts and never writes.
func applyURLPrecedence(flagURL, flagKey string) {
	envconfig.SetBaseURL(flagURL)
	envconfig.SetAPIKey(flagKey)
	cfg, err := config.Load()
	if err != nil {
		return
	}
	if cfg.BaseURL != "" {
		envconfig.SetConfigBaseURL(cfg.BaseURL)
	}
	if flagKey == "" && envconfig.EnvAPIKey() == "" && cfg.APIKey != nil {
		if resolved, err := cfg.APIKey.Resolve(); err == nil {
			envconfig.SetConfigAPIKey(resolved)
		}
	}
}

// deviceName is the label offered to the consent page, from the machine's
// hostname. The page decides the name it enrolls; this is a suggestion.
func deviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "prizmal-cli"
	}
	return host
}

// deviceCommands returns the `login` and `auth` subcommands.
func deviceCommands() []*cobra.Command {
	login := &cobra.Command{
		Use:   "login",
		Short: "Approve this machine in your browser, so prizmal claude needs no key",
		Long: `Sign in through your browser to approve this machine.

prizmal generates an ed25519 device key in ~/.prizmal and prints a URL and a
fingerprint. Open the URL, confirm the fingerprint matches, choose a router
config, and allow. The CLI then polls until the approval lands and stores the
first device token.

There is no localhost listener, so this works over SSH: open the printed URL
on any machine with a browser.

Running login again re-approves the device key this machine already has, which
is how a sign-in that expired is renewed. It never replaces the key.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          cmdLogin,
	}
	login.Flags().String("url", "", "provider base URL (or $"+envconfig.EnvVar+")")
	login.Flags().String("api-key", "", "provider API key (or $"+envconfig.KeyEnvVar+")")
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Device credential helpers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	token := &cobra.Command{
		Use:   "token",
		Short: "Print one device token (the Claude Code apiKeyHelper)",
		Long: `Print one device token on stdout and nothing else.

This is the command Claude Code runs as its apiKeyHelper. A cached token with
at least five minutes left is printed as is; otherwise the CLI signs a refresh
with the device key and prints the token the Switch returns. Diagnostics go to
stderr; stdout carries only the token, because Claude Code reads stdout as the
credential.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          cmdAuthToken,
	}
	token.Flags().String("url", "", "provider base URL (or $"+envconfig.EnvVar+")")
	token.Flags().String("api-key", "", "provider API key (or $"+envconfig.KeyEnvVar+")")
	auth.AddCommand(token)

	return []*cobra.Command{login, auth}
}
