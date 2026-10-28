package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
)

// Foreground commands now persist stats. Isolate every test and inherited child
// process from the user's real app-data directory, including older media tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shrink-test-stats-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SHRINK_DATA_DIR", dir); err != nil {
		os.RemoveAll(dir)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testApplication() *application {
	return newApplication(context.Background(), io.Discard, io.Discard)
}
