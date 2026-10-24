package test

import (
    "testing"
    "os/exec"
    "time"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// TestPerformance tests the performance of the FUSE daemon under typical loads
func TestPerformance(t *testing.T) {
    // Start the FUSE daemon
    daemonCmd := exec.Command("go", "run", "cmd/citcfs/main.go")
    err := daemonCmd.Start()
    if err != nil {
        t.Fatalf("Failed to start FUSE daemon: %v", err)
    }
    defer daemonCmd.Process.Kill()

    // Give the daemon some time to start
    time.Sleep(2 * time.Second)

    // Perform a series of operations to simulate typical loads
    for i := 0; i < 100; i++ {
        // Open file
        openCmd := exec.Command("cat", "/mnt/fuse/testfile")
        err := openCmd.Run()
        require.NoError(t, err, "Failed to open testfile")

        // Read file
        readCmd := exec.Command("cat", "/mnt/fuse/testfile")
        err = readCmd.Run()
        require.NoError(t, err, "Failed to read testfile")

        // Write to file
        writeCmd := exec.Command("echo", "New Content", ">", "/mnt/fuse/testfile")
        err = writeCmd.Run()
        require.NoError(t, err, "Failed to write to testfile")

        // Create a new file
        createCmd := exec.Command("touch", "/mnt/fuse/newfile")
        err = createCmd.Run()
        require.NoError(t, err, "Failed to create newfile")
    }

    // Check if the daemon is still responsive
    _, err = os.Stat("/mnt/fuse/testfile")
    assert.NoError(t, err, "Mount point should exist and be responsive")

    // Clean up
    os.RemoveAll("/mnt/fuse")
}