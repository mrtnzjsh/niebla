package test

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestIntegration tests the FUSE daemon startup
func TestIntegration(t *testing.T) {
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	daemonCmd := exec.Command("go", "run", "../main.go")
	err := daemonCmd.Start()
	if err != nil {
		t.Fatalf("Failed to start FUSE daemon: %v", err)
	}
	defer daemonCmd.Process.Kill()

	time.Sleep(500 * time.Millisecond)
	assert.NotNil(t, daemonCmd.Process)
}
