package prizmalcli

import (
	"os"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// TestMain keeps the tests away from the real ~/.prizmal and fails the package
// when they change it.
func TestMain(m *testing.M) { os.Exit(internaltest.Main(m)) }
