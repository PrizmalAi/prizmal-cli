package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
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

// apiKeyPrompt asks the first run for a key. It promises nothing about an
// empty answer, because there is nothing to promise: an empty answer saves
// no key and leaves no config file behind.
const apiKeyPrompt = "Enter your Prizmal API key: "

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
	return ensureConfigWith(readSecret, stdinIsTerminal())
}

// ensureConfigWith holds the body of ensureConfig with its two inputs
// supplied: how a key is read, and whether anyone is there to type one. Tests
// drive it with a scripted reader instead of a TTY.
//
// An empty answer returns a nil config and writes nothing. A key names a
// credential and a missing key names none, so there is nothing to persist; a
// file holding base_url alone would answer the next run's "does a config
// exist?" question with a yes that carries no key, and the prompt would never
// come back.
func ensureConfigWith(read keyReader, interactive bool) (*config.Config, error) {
	cfg, err := config.Load()
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, config.ErrNoConfig) {
		return nil, err
	}
	if !interactive {
		return nil, nil
	}

	printBanner(os.Stderr)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Welcome to Prizmal!")
	p, _ := config.Path()
	fmt.Fprintf(os.Stderr, "No configuration found at %s\n", p)
	key, err := read(apiKeyPrompt)
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}

	cfg = &config.Config{
		BaseURL: envconfig.DefaultURL,
		APIKey:  config.NewPlainAPIKey(key),
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return cfg, nil
}
