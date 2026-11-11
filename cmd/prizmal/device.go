package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
	"github.com/spf13/cobra"
)

// approvalURL asks the Switch where a person approves a device. The page
// belongs to the same deployment as the Switch, so the Switch reports it and
// the CLI holds no list of hosts, no environment setting and no default. A
// Switch that reports none stops the login: there is no other page to send the
// operator to.
func approvalURL(client *device.Client) (string, error) {
	url, err := client.ApprovalURL()
	if errors.Is(err, device.ErrNoApprovalPage) {
		return "", fmt.Errorf(
			"the Switch at %s doesn't report an approval page, so prizmal login can't tell where to approve this device; the CLI or the Switch needs updating",
			client.BaseURL)
	}
	return url, err
}

// prepareLogin does what a login needs before it prints a URL: it asks the
// Switch for the approval page, then settles the device key. The page comes
// first, so a Switch that reports none leaves no new key behind.
func prepareLogin(client *device.Client, out io.Writer) (string, *device.Key, bool, error) {
	appURL, err := approvalURL(client)
	if err != nil {
		return "", nil, false, err
	}
	key, created, err := ensureDeviceKey(client, out)
	if err != nil {
		return "", nil, false, err
	}
	return appURL, key, created, nil
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

// deviceLoginApplies decides whether the command about to run may use device
// login. A device token is a credential the CLI refreshes itself, so a --list,
// a bare --pick, and a launch of any harness with a refresh contract may use
// it. A runner without one cannot rotate the token and falls back to its
// ordinary key source. An unknown word is not a harness; the launch fails on it
// later either way.
func deviceLoginApplies(listFlag bool, args []string) bool {
	if listFlag || len(args) == 0 {
		return true
	}
	spec, err := registry.LookupIntegrationSpec(args[0])
	if err != nil {
		return true
	}
	return launcher.SupportsDeviceMode(spec.Runner)
}

// enterDeviceMode turns an enrolled device key into the launch's credential:
// it refreshes a device token and puts it in envconfig, where the catalog
// fetch and the launchers read it. It is a no-op when no device key exists.
//
// Device mode is set only once the refresh actually succeeds — never before
// the call, and never left on after a failed one — so a caller that reads
// envconfig.APIKey() here, with device mode still off, sees exactly the
// flag/env/config chain requireCredentialForRemote and announceKeySource see.
//
// A token that cannot be refreshed — the sign-in deadline passed, the device
// was revoked, the switch is unreachable — stops the launch only when there
// is nothing else to run with. A flag, $PRIZMAL_SWITCH_KEY, or the config
// file's api_key may already have resolved a working switch key before this
// runs (main.go resolves the config key first); device mode outranks that
// key when the device handshake works, not when it fails with a usable
// credential sitting right there. Silently proceeding on the fallback, with
// one warning, beats hard-erroring a launch that has a real key in hand.
//
// deviceLoginApplies decides whether this runs at all. A runner that cannot
// refresh a device token falls back to its ordinary key source.
func enterDeviceMode() error {
	if !deviceKeyExists() {
		return nil
	}
	key, err := device.LoadKey()
	if err != nil {
		return err
	}

	client := device.NewClient(envconfig.BaseURL())
	token, ct, err := device.HelperToken(client, key, time.Now(), loadCachedToken, time.Sleep)
	if err != nil {
		if fallback, src := envconfig.APIKey(), envconfig.APIKeySource(); fallback != "" {
			fmt.Fprintf(os.Stderr, "warning: device key could not refresh (%v); using the switch key from %s instead\n", err, src)
			return nil
		}
		if errors.Is(err, device.ErrReauthRequired) {
			return fmt.Errorf("this device's sign-in expired; run prizmal login to re-approve it")
		}
		if errors.Is(err, device.ErrDeviceRevoked) {
			return fmt.Errorf("this device was revoked; run prizmal login to enroll a new key")
		}
		if errors.Is(err, device.ErrNotAuthorized) || errors.Is(err, device.ErrDeviceUnknown) {
			return fmt.Errorf("device not authorized; run prizmal login to re-approve this device")
		}
		return err
	}

	envconfig.SetDeviceMode(true)
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
// comparing a fingerprint against a device the browser page never showed. The
// one exception is a revoked device, which the Switch refuses for good:
// ensureDeviceKey replaces that key.
func cmdLogin(cmd *cobra.Command, _ []string) error {
	flagURL, _ := cmd.Flags().GetString("url")
	flagKey, _ := cmd.Flags().GetString("api-key")
	if err := applyURLPrecedence(flagURL, flagKey); err != nil {
		return err
	}

	// Ask the Switch for the approval page before making a key: a login that
	// cannot tell where to approve must not leave a new device key behind.
	client := device.NewClient(envconfig.BaseURL())
	appURL, key, created, err := prepareLogin(client, os.Stderr)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSigning in with your browser...\n")
	if created {
		fmt.Fprintf(os.Stderr, "Generated a new device key at ~/.prizmal/%s\n", device.KeyFileName)
	} else {
		fmt.Fprintf(os.Stderr, "Re-approving the device key already on this machine.\n")
	}

	token, err := device.Login(client, key, device.LoginOptions{
		ApprovalURL: appURL,
		DeviceName:  deviceName(),
		Out:         os.Stderr,
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
	if err := applyURLPrecedence(flagURL, flagKey); err != nil {
		return err
	}

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
		if errors.Is(err, device.ErrDeviceRevoked) {
			return fmt.Errorf("this device was revoked; run prizmal login to enroll a new key")
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
//
// A config file that exists but cannot be read is an error, not a fallback to
// the default URL: signing in would otherwise approve a non-production device
// on the production page, the exact wrong answer when the file's base_url was
// unreadable.
func applyURLPrecedence(flagURL, flagKey string) error {
	envconfig.SetBaseURL(flagURL)
	envconfig.SetAPIKey(flagKey)
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNoConfig) {
			return nil
		}
		return fmt.Errorf("could not read the config file: %w", err)
	}
	if cfg.BaseURL != "" {
		envconfig.SetConfigBaseURL(cfg.BaseURL)
	}
	if flagKey == "" && envconfig.EnvAPIKey() == "" && cfg.APIKey != nil {
		if resolved, err := cfg.APIKey.Resolve(); err == nil {
			envconfig.SetConfigAPIKey(resolved)
		}
	}
	return nil
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
is how a sign-in that expired is renewed. It replaces the key only when the
Switch reports that the device was revoked: it asks whether to back the key up
(or backs it up without asking when there is no terminal), then makes a new key
and approves that.`,
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
