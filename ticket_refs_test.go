package prizmalcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ticketRefPattern matches a Linear ticket reference: the prefix and an id, with
// a word boundary at each end. It is written as a character class rather than a
// digit run so that no file quoting it holds a reference of its own, and the
// fixtures a reader writes by hand are assembled by ref() for the same reason.
var ticketRefPattern = regexp.MustCompile(`(?i)\bPRI-[0-9]+\b`)

// ref assembles a reference from its parts.
func ref(id string) string { return "P" + "RI-" + id }

// TestTicketRefPattern pins the matcher both ways. A scan that reads green
// because its pattern stopped matching reads exactly like a clean tree, and a
// pattern that flagged everything would teach people to ignore the check.
func TestTicketRefPattern(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{input: "// fixed under " + ref("2544"), want: []string{ref("2544")}},
		{input: "branch fix/" + strings.ToLower(ref("2827")), want: []string{strings.ToLower(ref("2827"))}},
		{input: "TODO(" + ref("NNNN") + ")"},
		{input: "the " + "su" + ref("2544") + " column"},
	}
	for _, tc := range cases {
		got := ticketRefPattern.FindAllString(tc.input, -1)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("FindAllString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestNoTicketRefsInCommittedFiles fails on a ticket reference in any file a
// commit would record. Git already links each line to the change that produced
// it, and the id is redundant on finished content. A reader of a public
// repository cannot open the ticket either. The pull-request body is where a
// ticket link belongs, because Linear's GitHub integration reads it there.
//
// Pass -count=1 after adding a file: go test caches this result, and a file that
// did not exist when the result was cached is not part of the cache key.
func TestNoTicketRefsInCommittedFiles(t *testing.T) {
	tracked, untracked := commitPaths(t)
	diverged := divergedPaths(t)

	for _, path := range tracked {
		for _, content := range trackedContents(t, path, diverged) {
			reportRefs(t, path, content)
		}
	}
	for _, path := range untracked {
		content, err := readAsCommitted(path)
		if err == nil {
			reportRefs(t, path, string(content))
		} else if !os.IsNotExist(err) {
			t.Fatalf("read %s: %v", path, err)
		}
	}
}

// reportRefs fails the test for every reference on every line of content.
func reportRefs(t *testing.T, path, content string) {
	t.Helper()
	for i, line := range strings.Split(content, "\n") {
		for _, found := range ticketRefPattern.FindAllString(line, -1) {
			t.Errorf("%s:%d has the ticket reference %s: %s", path, i+1, found, strings.TrimSpace(line))
		}
	}
}

// commitPaths returns what git would include in a commit: the index, and the
// untracked files .gitignore does not exclude. git draws the list rather than a
// directory walk, which would also read build output, and a built Go binary
// contains this pattern's string literals.
func commitPaths(t *testing.T) (tracked, untracked []string) {
	t.Helper()
	tracked = gitPaths(t, "ls-files", "-z", "--cached")
	untracked = gitPaths(t, "ls-files", "-z", "--others", "--exclude-standard")
	if len(tracked)+len(untracked) == 0 {
		t.Fatal("git listed no files: the check would pass without reading one")
	}
	return tracked, untracked
}

// divergedPaths returns the tracked paths whose working tree copy is not the
// staged one, a deletion from the disk included.
func divergedPaths(t *testing.T) map[string]bool {
	t.Helper()
	diverged := map[string]bool{}
	for _, path := range gitPaths(t, "diff", "--name-only", "-z") {
		diverged[path] = true
	}
	return diverged
}

// trackedContents returns the content a commit could record for a tracked path.
// A commit records the index, so the staged blob is read whenever the working
// tree copy has changed under it. That read covers a path staged and then
// deleted from the disk, which a scan over the working tree would pass. The
// working tree copy is read too, so an unstaged reference is reported.
func trackedContents(t *testing.T, path string, diverged map[string]bool) []string {
	t.Helper()
	worktree, err := readAsCommitted(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", path, err)
	}
	if err == nil && !diverged[path] {
		return []string{string(worktree)}
	}

	var contents []string
	if err == nil {
		contents = append(contents, string(worktree))
	}
	if blob := gitOutput(t, "show", "--end-of-options", ":"+path); len(contents) == 0 || contents[0] != blob {
		contents = append(contents, blob)
	}
	return contents
}

// readAsCommitted returns what a commit records for path: a file's content, or
// a symlink's target path. A symlink is read as its target and never followed,
// because the file it points to is a path of its own and gets its own read.
func readAsCommitted(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return []byte(target), err
	}
	return os.ReadFile(path)
}

// gitPaths returns the NUL-separated output of a git command as paths.
func gitPaths(t *testing.T, args ...string) []string {
	t.Helper()
	var paths []string
	for _, path := range strings.Split(gitOutput(t, args...), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// gitOutput runs git, and fails the test with git's own stderr when it fails. A
// read that did not happen is never a clean read.
func gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out)
}

// TestReadAsCommittedReadsASymlinkAsItsTarget pins how the scan reads a symlink:
// as the target path, which is what a commit records. A symlink to a directory,
// such as a skill linked into .claude/skills, is not a file os.ReadFile can
// read.
func TestReadAsCommittedReadsASymlinkAsItsTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	got, err := readAsCommitted(link)
	if err != nil {
		t.Fatalf("readAsCommitted: %v", err)
	}
	if string(got) != "target" {
		t.Fatalf("readAsCommitted = %q, want the link target %q", got, "target")
	}
}
