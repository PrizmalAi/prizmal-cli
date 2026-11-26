//go:build windows

package pi

import (
	"fmt"
	"os"
	"os/exec"
)

// linkPiEntry links one entry of the user's agent directory into the launch's
// agent directory.
//
// Windows grants the symlink privilege to administrators and, with Developer
// Mode on, to an ordinary user; without it os.Symlink fails, so this falls
// back to what needs no privilege. A file becomes a hard link, which Pi's
// in-place writes follow. A directory becomes a junction, which is what npm
// uses for the same purpose and which needs no privilege either.
func linkPiEntry(target, link string, isDir bool) error {
	err := os.Symlink(target, link)
	if err == nil {
		return nil
	}
	if isDir {
		return linkPiJunction(target, link, err)
	}
	if hardErr := os.Link(target, link); hardErr == nil {
		return nil
	}
	return err
}

// linkPiAbsentEntry links an entry the user's directory does not hold yet. A
// symlink is the only link that can precede its target, so without the symlink
// privilege there is nothing to do here: Pi creates the file in the launch
// directory instead, and it goes with the launch.
func linkPiAbsentEntry(target, link string) error {
	return os.Symlink(target, link)
}

// linkPiJunction makes a directory junction, the directory link Windows grants
// without a privilege. Its target must exist.
func linkPiJunction(target, link string, cause error) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (junction fallback: %v: %s)", cause, err, out)
	}
	return nil
}
