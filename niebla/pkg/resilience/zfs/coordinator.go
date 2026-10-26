package zfs

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Coordinator coordinates PostgreSQL backup state (pg_backup_start / pg_backup_stop)
// to guarantee atomic, crash-consistent ZFS filesystem snapshots across the cluster data.
type Coordinator struct {
	pool *pgxpool.Pool
}

// NewCoordinator creates a new PostgreSQL backup coordinator.
func NewCoordinator(pool *pgxpool.Pool) *Coordinator {
	return &Coordinator{pool: pool}
}

// StartBackup instructs PostgreSQL to begin a consistent base backup.
func (c *Coordinator) StartBackup(ctx context.Context, label string) (string, error) {
	if c.pool == nil {
		log.Printf("⚠️  [zfs-coordinator] PostgreSQL pool is nil, skipping pg_backup_start")
		return "", nil
	}
	var lsn string
	// pg_backup_start(label text [, fast boolean ]) returns pg_lsn
	err := c.pool.QueryRow(ctx, "SELECT pg_backup_start($1, false)", label).Scan(&lsn)
	if err != nil {
		return "", fmt.Errorf("failed to initiate PostgreSQL backup state: %w", err)
	}
	return lsn, nil
}

// StopBackup signals PostgreSQL that the ZFS snapshot has completed.
func (c *Coordinator) StopBackup(ctx context.Context) (string, error) {
	if c.pool == nil {
		return "", nil
	}
	var lsn string
	// pg_backup_stop([wait_for_archive boolean]) returns pg_lsn
	err := c.pool.QueryRow(ctx, "SELECT pg_backup_stop(true)").Scan(&lsn)
	if err != nil {
		return "", fmt.Errorf("failed to terminate PostgreSQL backup state: %w", err)
	}
	return lsn, nil
}
