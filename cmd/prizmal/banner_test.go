package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// bannerArt is the banner as it must appear, character for character. It is
// written out again here rather than derived, because the point of the test is
// that the two halves in firstrun.go still add up to this: the art carries a
// "do not modify it" note, and splitting it for colouring must not change it.
const bannerArt = `     ▲        ______     ______       _      ______   __  __        _        _
    / \      |  ___ \   |  ___ \     | |    |___  /  |  \/  |      / \      | |
   /   \     | |___) |  | |___) |    | |       / /   | \  / |     / ▲ \     | |
  /     \    |  ____/   |  __  /     | |      / /    | |\/| |    / / \ \    | |
 /       \   | |        | |  \ \     | |     / /__   | |  | |   / /___\ \   | |____
/_________\  |_|        |_|   \_\    |_|    /_____|  |_|  |_|  /_/     \_\  |______|
`

// The two halves reassemble into the banner exactly, trailing newline included.
func TestBannerHalvesReassemble(t *testing.T) {
	if got := prizmalBanner(); got != bannerArt {
		t.Errorf("prizmalBanner() does not match the art\n got:\n%s\nwant:\n%s", got, bannerArt)
	}
}

// The halves are the same height, so reassembling cannot drop or duplicate a
// row.
func TestBannerHalvesHaveEqualHeight(t *testing.T) {
	logo := strings.Count(prizmalLogo, "\n") + 1
	wordmark := strings.Count(prizmalWordmark, "\n") + 1
	if logo != wordmark {
		t.Fatalf("logo is %d lines and wordmark is %d; they must pair line for line", logo, wordmark)
	}
}

// The banner is printed with the triangle in the brand green and the wordmark
// plain, on every line of the triangle.
func TestPrintBannerGreensTheLogo(t *testing.T) {
	original := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })
	lipgloss.SetColorProfile(termenv.TrueColor)

	var buf bytes.Buffer
	printBanner(&buf)
	out := buf.String()

	// The green opens the logo on each of its lines.
	if got := strings.Count(out, "\x1b[38;2;88;124;95m"); got != 6 {
		t.Errorf("the logo is coloured on %d lines, want 6", got)
	}
	// The wordmark carries no colour of its own.
	if strings.Contains(out, "\x1b[1m") {
		t.Error("the banner is bold somewhere; only the logo should be coloured")
	}

	// Stripping the escape leaves the art untouched.
	stripped := stripANSI(out)
	if stripped != bannerArt {
		t.Errorf("the printed banner is not the art once colour is removed\n got:\n%s\nwant:\n%s", stripped, bannerArt)
	}
}

// stripANSI removes SGR escape sequences so the art can be compared.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
