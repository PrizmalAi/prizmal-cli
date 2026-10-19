package launch

import "strings"

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
