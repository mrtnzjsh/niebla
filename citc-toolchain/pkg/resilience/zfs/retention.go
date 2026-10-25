package zfs

import (
	"context"
	"fmt"
	"strings"
)

// PruneOlderSnapshots destroys snapshots exceeding retentionCount that match prefix.
func PruneOlderSnapshots(ctx context.Context, mgr *SnapshotManager, dataset, prefix string, retentionCount int) (pruned []string, err error) {
	if retentionCount <= 0 {
		return nil, nil
	}

	snapshots, err := mgr.ListSnapshots(ctx, dataset)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots for retention pruning: %w", err)
	}

	// Filter to snapshots matching prefix
	var matching []SnapshotInfo
	for _, snap := range snapshots {
		if prefix == "" || strings.HasPrefix(snap.Tag, prefix) {
			matching = append(matching, snap)
		}
	}

	if len(matching) <= retentionCount {
		return nil, nil
	}

	// Snapshots are sorted chronologically by ListSnapshots
	excessCount := len(matching) - retentionCount
	toDestroy := matching[:excessCount]

	for _, snap := range toDestroy {
		if err := mgr.DestroySnapshot(ctx, snap.Name); err != nil {
			return pruned, fmt.Errorf("failed to prune snapshot %s: %w", snap.Name, err)
		}
		pruned = append(pruned, snap.Name)
	}

	return pruned, nil
}
