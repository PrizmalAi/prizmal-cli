package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// readmeUsageBlock returns the fenced code block under the README's "## Usage"
// heading — the block that presents itself to a reader as `prizmal --help`.
func readmeUsageBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	// Git checks the README out with CRLF endings on Windows, so normalise
	// before splitting: the exact-match checks below would otherwise fail on
	// a trailing \r that no reader of the README can see.
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	i := 0
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "## Usage" {
			break
		}
	}
	if i == len(lines) {
		t.Fatal("README.md has no `## Usage` heading")
	}
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "```") {
			i++
			break
		}
	}
	var block []string
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "```") {
			return strings.Join(block, "\n")
		}
		block = append(block, lines[i])
	}
	t.Fatal("README.md `## Usage` code fence is never closed")
	return ""
}

// TestREADMEUsageDocumentsEveryRegisteredFlag pins the README's Usage block to
// the flags prizmal actually registers. The README is a release artifact: it is
// the first thing a new operator reads, and a flag missing from it is invisible
// even though the binary accepts it. --allow-unauthenticated was exactly that —
// described in prose but absent from the flag list.
//
// Scope is the flags prizmal declares in registerFlags. Cobra's built-in --help
// and --version are deliberately not covered; they are not ours to drift.
func TestREADMEUsageDocumentsEveryRegisteredFlag(t *testing.T) {
	block := readmeUsageBlock(t)

	flags := pflag.NewFlagSet("prizmal", pflag.ContinueOnError)
	registerFlags(flags)

	flags.VisitAll(func(f *pflag.Flag) {
		line := flagEntryLine(block, f.Name)
		if line == "" {
			t.Errorf("README `## Usage` block does not document --%s\n"+
				"the binary accepts it; the docs never mention it", f.Name)
			return
		}
		if f.Shorthand != "" && !strings.Contains(line, "-"+f.Shorthand+", --"+f.Name) {
			t.Errorf("README documents --%s without its shorthand -%s", f.Name, f.Shorthand)
		}
		// The description is checked on the flag's own entry line, so a match
		// under some other flag does not count. It is still a substring test:
		// the README may add detail after the binary's wording (--model's
		// "(defaults to prizmal/default)"), which a full-equality check would
		// reject. So a truncated description on the right line still passes.
		if !strings.Contains(line, f.Usage) {
			t.Errorf("README description for --%s has drifted from the binary\n got line: %q\nwant help text: %q", f.Name, line, f.Usage)
		}
	})
}

// flagEntryLine returns the block line that lists the given long flag as its
// own entry — the line whose first token after the optional `-x, ` shorthand is
// `--name`. Returns "" when the flag has no entry, so a mention inside another
// flag's description ("persistent until --restore") is not mistaken for one.
func flagEntryLine(block, name string) string {
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(line)
		for _, tok := range fields {
			tok = strings.TrimSuffix(tok, ",")
			if tok == "--"+name {
				return line
			}
			if !strings.HasPrefix(tok, "-") {
				break
			}
		}
	}
	return ""
}

// TestUsageLineKeepsFlagsBeforeTheIntegration pins the shape of the first line
// of `prizmal --help`. Every argument after the integration name is handed to
// the harness unparsed, so a usage line reading `prizmal [INTEGRATION] [flags]
// [-- EXTRA_ARGS...]` tells the reader to put prizmal's own flags exactly
// where prizmal will not see them.
//
// Cobra appends " [flags]" to the use line only when the string does not
// already contain "[flags]", so usageLine spelling the token itself is what
// keeps the order right. Asserting on UseLine() rather than on the constant
// means this test also catches cobra changing that rule under us.
func TestUsageLineKeepsFlagsBeforeTheIntegration(t *testing.T) {
	cmd := &cobra.Command{Use: usageLine}
	registerFlags(cmd.Flags())

	got := cmd.UseLine()
	if got != usageLine {
		t.Errorf("cobra rewrote the use line\n got: %q\nwant: %q", got, usageLine)
	}
	flagsAt := strings.Index(got, "[flags]")
	integrationAt := strings.Index(got, "[INTEGRATION]")
	sepAt := strings.Index(got, "[-- ")
	if flagsAt < 0 || integrationAt < 0 || sepAt < 0 {
		t.Fatalf("use line %q lost [flags], [INTEGRATION] or the -- separator", got)
	}
	if flagsAt > integrationAt {
		t.Errorf("use line puts [flags] after the integration name: %q\n"+
			"flags placed there are passed to the harness, not parsed by prizmal", got)
	}
	if flagsAt > sepAt {
		t.Errorf("use line puts [flags] after the `--` separator: %q", got)
	}
}

// TestREADMEUsageLineMatchesTheBinary keeps the README's first Usage line
// identical to what the binary prints, so the ordering fixed above cannot drift
// back in the docs alone.
func TestREADMEUsageLineMatchesTheBinary(t *testing.T) {
	block := readmeUsageBlock(t)
	first, _, _ := strings.Cut(block, "\n")
	if first != usageLine {
		t.Errorf("README `## Usage` first line has drifted from the binary\n got: %q\nwant: %q", first, usageLine)
	}
}

// TestREADMEUsageDocumentsOnlyRegisteredFlags is the reverse of the check
// above: every `--flag` token the README lists must still exist in the binary,
// so deleting a flag from registerFlags cannot leave a stale README entry
// advertising it. Cobra's built-ins are allowed through — they are real flags,
// just not ours.
func TestREADMEUsageDocumentsOnlyRegisteredFlags(t *testing.T) {
	block := readmeUsageBlock(t)

	flags := pflag.NewFlagSet("prizmal", pflag.ContinueOnError)
	registerFlags(flags)

	cobraBuiltins := map[string]bool{"help": true, "version": true}

	for _, name := range flagTokens.FindAllStringSubmatch(block, -1) {
		if cobraBuiltins[name[1]] {
			continue
		}
		if flags.Lookup(name[1]) == nil {
			t.Errorf("README `## Usage` block documents --%s, which prizmal does not register\n"+
				"either the flag was deleted and the docs kept it, or it is spelled wrong", name[1])
		}
	}
}

// flagTokens matches a long-flag token: `--` followed by a lowercase word. It
// deliberately does not match the bare `--` separator in `[-- EXTRA_ARGS...]`.
var flagTokens = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)
