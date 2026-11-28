//go:build !windows

package pi

import "os"

// linkPiEntry links one entry of the user's agent directory into the launch's
// agent directory. A symlink works for a file or a directory, and every OS
// this build runs on lets a process create one.
func linkPiEntry(target, link string, isDir bool) error {
	return os.Symlink(target, link)
}

// linkPiAbsentEntry links an entry the user's directory does not hold yet. The
// target need not exist for a symlink, so the link is there for whatever Pi
// creates later.
func linkPiAbsentEntry(target, link string) error {
	return os.Symlink(target, link)
}
