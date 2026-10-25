package db

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/0001_init.sql
var initSQL string

//go:embed migrations/0002_critique.sql
var critiqueSQL string

//go:embed migrations/0003_file_reviews.sql
var fileReviewsSQL string

// DB wraps the pgxpool connection pool and provides access to database queries.
type DB struct {
	pool *pgxpool.Pool
}

// Config holds configuration options for the database connection pool.
type Config struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

// DefaultConfig returns sensible defaults optimized for high-frequency reads.
func DefaultConfig(url string) Config {
	return Config{
		URL:               url,
		MaxConns:          50,
		MinConns:          5,
		MaxConnLifetime:   1 * time.Hour,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
	}
}

// Connect establishes a connection pool to PostgreSQL and runs embedded migrations.
func Connect(ctx context.Context, cfg Config) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database url: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create pgx connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &DB{pool: pool}
	if err := database.RunMigrations(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	return database, nil
}

// RunMigrations executes embedded SQL migrations to ensure schema is initialized.
func (d *DB) RunMigrations(ctx context.Context) error {
	if _, err := d.pool.Exec(ctx, initSQL); err != nil {
		return fmt.Errorf("failed to execute schema migration: %w", err)
	}
	if _, err := d.pool.Exec(ctx, critiqueSQL); err != nil {
		return fmt.Errorf("failed to execute critique schema migration: %w", err)
	}
	if _, err := d.pool.Exec(ctx, fileReviewsSQL); err != nil {
		return fmt.Errorf("failed to execute file reviews schema migration: %w", err)
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Close gracefully closes the connection pool.
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// Ping checks whether the database is responsive.
func (d *DB) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}
