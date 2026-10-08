package codex

import (
	"slices"
	"testing"
)

// TestCodexArgsRunWithoutTheSharedServer pins --no-daemon. Codex starts
// without its shared background server whenever a launch passes --profile or
// a -c override, and it prints a footer warning for that unless the launch
// says so. The flag is a root option, so it goes ahead of any subcommand.
func TestCodexArgsRunWithoutTheSharedServer(t *testing.T) {
	args, err := (&Codex{}).args("prizmal-flash", "/tmp/catalog.json", []string{"exec", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	flag := slices.Index(args, "--no-daemon")
	if flag < 0 {
		t.Fatalf("args lack --no-daemon: %v", args)
	}
	if sub := slices.Index(args, "exec"); sub < flag {
		t.Fatalf("--no-daemon at %d comes after the subcommand at %d: %v", flag, sub, args)
	}
}
