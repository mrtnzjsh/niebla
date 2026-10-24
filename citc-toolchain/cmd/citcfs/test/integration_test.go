package test

import (
    "testing"
    "os/exec"
    "os"
    "net/http"
    "time"
    "io/ioutil"
    "github.com/stretchr/testify/assert"
)

// TestIntegration tests the entire FUSE daemon setup
func TestIntegration(t *testing.T) {
    // Start the FUSE daemon
    daemonCmd := exec.Command("go", "run", "cmd/citcfs/main.go")
    err := daemonCmd.Start()
    if err != nil {
        t.Fatalf("Failed to start FUSE daemon: %v", err)
    }
    defer daemonCmd.Process.Kill()

    // Give the daemon some time to start
    time.Sleep(2 * time.Second)

    // Check if the mount point exists
    _, err = os.Stat("/mnt/fuse")
    assert.NoError(t, err, "Mount point should exist")

    // Perform some basic operations
    resp, err := http.Get("http://localhost:8080/testfile")
    assert.NoError(t, err, "Failed to get testfile")
    defer resp.Body.Close()

    body, err := ioutil.ReadAll(resp.Body)
    assert.NoError(t, err, "Failed to read testfile body")
    assert.Equal(t, "Test Content", string(body), "Incorrect file content")

    // Clean up
    os.RemoveAll("/mnt/fuse")
}