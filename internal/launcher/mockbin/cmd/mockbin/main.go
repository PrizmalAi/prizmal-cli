// Command mockbin is the test mock harness binary. It is not meant to be
// installed; the test suite builds it with `go build` and copies it onto a
// temp PATH under each harness name. See package mockbin for behavior.
package main

import (
	"os"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/mockbin"
)

func main() {
	os.Exit(mockbin.Run())
}
