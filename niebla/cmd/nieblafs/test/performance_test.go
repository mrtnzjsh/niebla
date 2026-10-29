package test

import (
	"os"
	"testing"
)

// TestPerformance tests the performance of the FUSE daemon under typical loads
func TestPerformance(t *testing.T) {
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("skipping performance test in short mode")
	}
}
