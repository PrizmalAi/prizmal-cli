package launch

import (
	"fmt"
	"strings"
)

// FirstPositional returns the first argument that is not a flag or a flag's
// value, which is how a harness names a subcommand.
//
// The harness flags prizmal does not own reach it unchanged, so the words after
// the integration name are the harness's own grammar. A runner that decides
// whether a launch is headless from a subcommand reads that grammar instead of
// guessing: `codex exec hi` runs one turn, while `codex -C exec hi` opens an
// interactive session in a directory named exec.
//
// valueFlags lists the flags whose value is the next argument. A flag written
// with `=` carries its own value and is skipped whole. Everything after a
// literal `--` is the harness's own, so it is not a subcommand.
func FirstPositional(args []string, valueFlags map[string]bool) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return ""
		case strings.HasPrefix(arg, "-") && arg != "-":
			name := arg
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				name = arg[:eq]
			}
			if valueFlags[name] && !strings.Contains(arg, "=") {
				// The next argument is this flag's value, not a subcommand.
				i++
			}
		default:
			return arg
		}
	}
	return ""
}

// SplitArgs splits a configured argument string into words the way a shell
// does for the cases an operator writes by hand: whitespace separates words,
// single quotes keep their contents literal, and double quotes keep their
// contents together and let a backslash escape a double quote or a backslash.
// A backslash outside quotes is literal, so a Windows path survives.
//
// The string is free text. SplitArgs never asks whether a word is a flag the
// harness knows.
func SplitArgs(s string) ([]string, error) {
	var (
		words   []string
		word    strings.Builder
		inWord  bool
		quote   rune
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			if r != '"' && r != '\\' {
				word.WriteRune('\\')
			}
			word.WriteRune(r)
			escaped = false
		case quote == '"' && r == '\\':
			escaped = true
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if escaped {
		word.WriteRune('\\')
	}
	if inWord || escaped {
		words = append(words, word.String())
	}
	return words, nil
}

// MergeMissingFlags appends the configured arguments that args does not
// already carry.
//
// A configured flag and the words that follow it, up to the next flag, form
// one group. The group is skipped when args holds the same flag, spelled
// `--flag` or `--flag=value`, so the invocation always wins over the config.
// Words before the first flag belong to no flag and are always kept. A flag
// that appears twice in the configured arguments is kept twice, because the
// check reads args and not the arguments already merged.
//
// The merge reads flag syntax only. It does not know which flags take a value
// or which a harness accepts.
func MergeMissingFlags(args, configured []string) []string {
	present := map[string]bool{}
	for _, arg := range args {
		if isFlag(arg) {
			present[flagName(arg)] = true
		}
	}
	out := append([]string(nil), args...)
	keep := true
	for _, word := range configured {
		if isFlag(word) {
			keep = !present[flagName(word)]
		}
		if keep {
			out = append(out, word)
		}
	}
	return out
}

func isFlag(word string) bool { return strings.HasPrefix(word, "-") && word != "-" }

func flagName(word string) string {
	name, _, _ := strings.Cut(word, "=")
	return name
}
