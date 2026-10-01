package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// prizmalBanner is the Prizmal triangle logo with the Prizmal name in
// block letters, printed verbatim at first run. Do not modify it.
//
// It is split into two pieces so the logo can be coloured without touching the
// art: prizmalLogo is the triangle drawn in columns 0-10, and prizmalWordmark
// is everything from column 11 rightwards. Concatenating them line by line
// reproduces the banner exactly (see TestBannerHalvesReassemble).
const prizmalLogo = `     ▲     
    / \    
   /   \   
  /     \  
 /       \ 
/_________\`

const prizmalWordmark = `   ______     ______       _      ______   __  __        _        _
  |  ___ \   |  ___ \     | |    |___  /  |  \/  |      / \      | |
  | |___) |  | |___) |    | |       / /   | \  / |     / ▲ \     | |
  |  ____/   |  __  /     | |      / /    | |\/| |    / / \ \    | |
  | |        | |  \ \     | |     / /__   | |  | |   / /___\ \   | |____
  |_|        |_|   \_\    |_|    /_____|  |_|  |_|  /_/     \_\  |______|`

// prizmalBanner reassembles the two halves into the banner printed at first
// run. It exists so the art stays one string to read and to test, while the
// logo can still be styled on its own.
func prizmalBanner() string {
	logo := strings.Split(prizmalLogo, "\n")
	wordmark := strings.Split(prizmalWordmark, "\n")

	lines := make([]string, 0, len(logo))
	for i := range logo {
		lines = append(lines, logo[i]+wordmark[i])
	}
	return strings.Join(lines, "\n") + "\n"
}

// bannerLogoStyle colours the logo, matching the picker's selected row. It
// reads the colour from the launcher so the banner and the picker cannot drift
// apart.
func bannerLogoStyle() lipgloss.Style {
	return launcher.BrandLogoStyle()
}

// printBanner writes the banner with the triangle in the Prizmal green and the
// wordmark unstyled, so the logo reads as a mark rather than as more text.
func printBanner(w io.Writer) {
	logo := strings.Split(prizmalLogo, "\n")
	wordmark := strings.Split(prizmalWordmark, "\n")

	style := bannerLogoStyle()
	for i := range logo {
		// A write to stderr fails when the reader closes the pipe early, which
		// is not a reason to abandon the prompt behind it.
		_, _ = fmt.Fprintln(w, style.Render(logo[i])+wordmark[i])
	}
}

// firstRunSignInHeading is the first-run picker's title. The picker is the same
// widget as the model picker, so the first run presents its two ways to sign in
// as two rows rather than a numbered prompt.
const firstRunSignInHeading = "Sign in"

// firstRunSignInOptions are the two credentials this CLI accepts: a
// browser-approved device key, which needs no typing and works over SSH, or a
// switch key pasted in. The Value is what the picker returns and what the
// switch below matches on.
func firstRunSignInOptions() []launcher.Option {
	return []launcher.Option{
		{
			Label:       "Sign in with your browser",
			Value:       "browser",
			Description: "Approve this machine in your browser",
		},
		{
			Label:       "Paste a key",
			Value:       "key",
			Description: "Use a Prizmal Switch API key",
		},
	}
}

// apiKeyPrompt asks the first run for a key. It promises nothing about an
// empty answer, because there is nothing to promise: an empty answer saves
// no key and leaves no config file behind.
const apiKeyPrompt = "Enter your Prizmal API key: "

// signInBrowser is the seam for the browser path of the first-run menu. It is
// a variable so a test can drive the menu without a browser or a network:
// production points it at runBrowserSignIn.
var signInBrowser = runBrowserSignIn

// menuPicker renders the first-run picker and returns the chosen value. It is
// the launcher's picker in production; a test supplies a fake so it never opens
// a real terminal.
type menuPicker func(heading string, options []launcher.Option) (string, error)

// cancelledPicker is the test seam's picker: it reports a backed-out menu, so a
// test that does not supply its own never reaches a terminal.
func cancelledPicker(string, []launcher.Option) (string, error) { return "", launcher.ErrCancelled }

// stdinIsTerminal reports whether stdin is an interactive terminal.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// keyReader reads one secret, printing prompt first and echoing nothing. It
// exists as a seam: production reads the real terminal through readSecret, and
// a test supplies a reader over a string so it never touches os.Stdin.
type keyReader func(prompt string) (string, error)

// readSecret reads an API key from the terminal without echoing. It uses
// term.ReadPassword on a TTY and falls back to a plain line read so scripts
// never block on a prompt nobody is there to answer.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r"), nil
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		return "", nil
	}
	return strings.TrimSpace(line), err
}

// ensureConfig returns the loaded Prizmal config, creating one via the
// first-run prompt when no config file exists and stdin is interactive. When
// stdin is not a terminal it returns a nil config without prompting so scripts
// and tests never block or write a config.
func ensureConfig() (*config.Config, error) {
	return ensureConfigWithMenu(readSecret, stdinIsTerminal(), launcher.PickOption, signInBrowser)
}

// ensureConfigWith holds the body of ensureConfig with its inputs supplied: how
// a key is read, and whether anyone is there to type one.
//
// Its picker reports a backed-out menu and its browser sign-in is a no-op: this
// is the seam tests drive, and a test must never be able to open the operator's
// terminal or browser. A test that means to exercise either path calls
// ensureConfigWithMenu with its own fake.
func ensureConfigWith(read keyReader, interactive bool) (*config.Config, error) {
	return ensureConfigWithMenu(read, interactive, cancelledPicker, func() error { return nil })
}

// ensureConfigWithMenu is ensureConfigWith with the picker and the browser
// sign-in supplied.
func ensureConfigWithMenu(read keyReader, interactive bool, pick menuPicker, browserSignIn func() error) (*config.Config, error) {
	cfg, err := config.Load()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, config.ErrNoConfig) {
		return nil, err
	}
	// A machine that already signed in with a device key has no config file to
	// create: the credential is the device key. Asking the first-run menu again
	// would offer to replace what is already working.
	if deviceKeyExists() {
		return nil, nil
	}
	if !interactive {
		return nil, nil
	}

	printBanner(os.Stderr)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Welcome to Prizmal!")
	p, _ := config.Path()
	fmt.Fprintf(os.Stderr, "No configuration found at %s\n", p)

	choice, err := pick(firstRunSignInHeading, firstRunSignInOptions())
	if err != nil {
		// A backed-out menu is the operator's decision, not a failure: nothing is
		// saved and the next run asks again.
		if errors.Is(err, launcher.ErrCancelled) {
			return nil, nil
		}
		return nil, err
	}
	switch choice {
	case "browser":
		if err := browserSignIn(); err != nil {
			return nil, err
		}
		// The device key, not a config file, is the credential the browser
		// path leaves behind.
		envconfig.SetDeviceMode(true)
		return nil, nil
	case "key":
		return configFromPastedKey(read)
	default:
		// The picker only ever returns one of its own option values, so this is
		// unreachable. Refusing rather than guessing keeps a browser from opening
		// on an unexpected value.
		return nil, fmt.Errorf("unexpected sign-in choice %q", choice)
	}
}

// configFromPastedKey reads a switch key and writes the config file, the way
// the first run always did. An empty answer writes nothing.
func configFromPastedKey(read keyReader) (*config.Config, error) {
	key, err := read(apiKeyPrompt)
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	cfg := &config.Config{
		BaseURL: envconfig.DefaultURL,
		APIKey:  config.NewPlainAPIKey(key),
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// runBrowserSignIn is the first-run menu's browser path: it enrolls this
// machine the way `prizmal login` does, reusing the same key and loop.
func runBrowserSignIn() error {
	key, created, err := device.LoadOrCreateKey()
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(os.Stderr, "Generated a device key at ~/.prizmal/%s\n", device.KeyFileName)
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
	return (&device.CachedToken{Token: token.Value, Expiry: token.Expiry, ReauthBy: token.ReauthBy}).Save()
}
