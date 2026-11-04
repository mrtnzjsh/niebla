package zfs

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SnapshotInfo describes a ZFS snapshot.
type SnapshotInfo struct {
	Name      string    `json:"name"`
	Dataset   string    `json:"dataset"`
	Tag       string    `json:"tag"`
	CreatedAt time.Time `json:"created_at"`
	UsedBytes int64     `json:"used_bytes"`
}

// SnapshotManager interacts with the ZFS CLI to manage snapshots.
type SnapshotManager struct {
	zfsPath string
}

// NewSnapshotManager creates a SnapshotManager instance.
func NewSnapshotManager(zfsPath ...string) *SnapshotManager {
	binaryPath := "zfs"
	if len(zfsPath) > 0 && zfsPath[0] != "" {
		binaryPath = zfsPath[0]
	}
	return &SnapshotManager{zfsPath: binaryPath}
}

// CreateRecursiveSnapshot creates an atomic recursive snapshot across dataset and all child datasets.
func (m *SnapshotManager) CreateRecursiveSnapshot(ctx context.Context, dataset, snapTag string) error {
	fullSnap := fmt.Sprintf("%s@%s", dataset, snapTag)
	cmd := exec.CommandContext(ctx, m.zfsPath, "snapshot", "-r", fullSnap)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs snapshot %s failed: %w (output: %s)", fullSnap, err, string(out))
	}
	return nil
}

// ListSnapshots returns all snapshots for a given dataset sorted by creation time.
func (m *SnapshotManager) ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error) {
	cmd := exec.CommandContext(ctx, m.zfsPath, "list", "-H", "-p", "-o", "name,creation,used", "-t", "snapshot", "-r", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("zfs list snapshots for %s failed: %w (output: %s)", dataset, err, string(out))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var snapshots []SnapshotInfo

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		creationEpoch, _ := strconv.ParseInt(parts[1], 10, 64)
		usedBytes, _ := strconv.ParseInt(parts[2], 10, 64)

		subParts := strings.SplitN(name, "@", 2)
		tag := ""
		ds := subParts[0]
		if len(subParts) == 2 {
			tag = subParts[1]
		}

		snapshots = append(snapshots, SnapshotInfo{
			Name:      name,
			Dataset:   ds,
			Tag:       tag,
			CreatedAt: time.Unix(creationEpoch, 0).UTC(),
			UsedBytes: usedBytes,
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})

	return snapshots, nil
}

// DestroySnapshot recursively destroys a snapshot.
func (m *SnapshotManager) DestroySnapshot(ctx context.Context, snapshot string) error {
	cmd := exec.CommandContext(ctx, m.zfsPath, "destroy", "-r", snapshot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs destroy %s failed: %w (output: %s)", snapshot, err, string(out))
	}
	return nil
}

// Rollback rolls back a dataset to the specified snapshot.
func (m *SnapshotManager) Rollback(ctx context.Context, snapshot string) error {
	cmd := exec.CommandContext(ctx, m.zfsPath, "rollback", "-r", snapshot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs rollback %s failed: %w (output: %s)", snapshot, err, string(out))
	}
	return nil
}
