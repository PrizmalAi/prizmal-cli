package main

import (
	"os"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// TestMain keeps the developer's exported Switch key and URL out of the tests.
func TestMain(m *testing.M) { os.Exit(internaltest.Main(m)) }
