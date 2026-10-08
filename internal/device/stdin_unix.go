//go:build unix

package device

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinReady waits up to a short slice for input on stdin and reports whether a
// read would now return. A false answer lets the caller check whether it was
// told to stop, so the wait ends without a read left pending on the terminal.
func stdinReady() (bool, error) {
	fds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, 100)
		if err == unix.EINTR {
			continue
		}
		return n > 0, err
	}
}
