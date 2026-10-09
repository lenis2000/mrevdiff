package main

import (
	"os"
	"testing"
)

// TestMain keeps the agterm integration off for the whole package: runDiff
// marks, flags and restores the agterm session it runs in, and a test run
// from inside agterm must not do that to the session it was started from.
func TestMain(m *testing.M) {
	os.Setenv("MREVDIFF_AGTERM", "0")
	os.Exit(m.Run())
}
