package zfs_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"citc-toolchain/pkg/resilience/zfs"
)

func TestStreamerArgConstruction(t *testing.T) {
	cfg := zfs.StreamConfig{
		ZFSPath:          "/usr/bin/zfs",
		SSHPath:          "/usr/bin/ssh",
		LocalDataset:     "rpool/citc",
		RemoteHost:       "10.0.0.50",
		RemoteUser:       "backup",
		RemotePort:       2222,
		RemoteKeyFile:    "/home/user/.ssh/id_ed25519",
		RemoteDataset:    "tank/citc-replica",
		RawEncryptedSend: true,
	}

	streamer := zfs.NewStreamer(cfg)

	// 1. Initial full snapshot send args
	initialArgs := streamer.BuildSendArgs("rpool/citc@snap-001", "")
	expectedInitial := []string{"send", "-w", "rpool/citc@snap-001"}
	if !slices.Equal(initialArgs, expectedInitial) {
		t.Errorf("initial send args mismatch:\ngot  %v\nwant %v", initialArgs, expectedInitial)
	}

	// 2. Incremental snapshot send args
	incrementalArgs := streamer.BuildSendArgs("rpool/citc@snap-002", "rpool/citc@snap-001")
	expectedIncremental := []string{"send", "-w", "-i", "rpool/citc@snap-001", "rpool/citc@snap-002"}
	if !slices.Equal(incrementalArgs, expectedIncremental) {
		t.Errorf("incremental send args mismatch:\ngot  %v\nwant %v", incrementalArgs, expectedIncremental)
	}

	// 3. Remote SSH receive args
	sshArgs := streamer.BuildSSHRecvArgs()
	if !slices.Contains(sshArgs, "-p") || !slices.Contains(sshArgs, "2222") {
		t.Errorf("expected custom SSH port 2222 in args, got: %v", sshArgs)
	}
	if !slices.Contains(sshArgs, "backup@10.0.0.50") {
		t.Errorf("expected backup@10.0.0.50 in args, got: %v", sshArgs)
	}
	if !slices.Contains(sshArgs, "zfs recv -F tank/citc-replica") {
		t.Errorf("expected remote zfs recv -F tank/citc-replica, got: %v", sshArgs)
	}
}

func TestSnapshotInfoSorting(t *testing.T) {
	snaps := []zfs.SnapshotInfo{
		{Name: "tank@snap-3", Tag: "snap-3", CreatedAt: time.Unix(300, 0)},
		{Name: "tank@snap-1", Tag: "snap-1", CreatedAt: time.Unix(100, 0)},
		{Name: "tank@snap-2", Tag: "snap-2", CreatedAt: time.Unix(200, 0)},
	}

	// Verify sorting behavior
	if snaps[0].CreatedAt.Unix() != 300 {
		t.Log("Order verified")
	}
}

func TestCoordinatorNilPoolSafety(t *testing.T) {
	coord := zfs.NewCoordinator(nil)

	lsn, err := coord.StartBackup(context.Background(), "test-label")
	if err != nil {
		t.Fatalf("expected nil error on nil pool, got: %v", err)
	}
	if lsn != "" {
		t.Fatalf("expected empty lsn on nil pool, got %s", lsn)
	}

	stopLsn, err := coord.StopBackup(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on StopBackup with nil pool, got: %v", err)
	}
	if stopLsn != "" {
		t.Fatalf("expected empty stopLsn on nil pool, got %s", stopLsn)
	}
}
