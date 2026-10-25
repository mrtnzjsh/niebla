package drivers

import (
	"context"
	"io"
	"time"
)

// ItemInfo describes a remote or local stored backup item.
type ItemInfo struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
}

// StorageDriver defines the common interface for backup storage destinations.
type StorageDriver interface {
	// Name returns the driver identifier (e.g. "filesystem", "nextcloud", "s3").
	Name() string

	// Upload streams data to the destination under relativePath.
	Upload(ctx context.Context, relativePath string, r io.Reader, size int64) error

	// Download streams data from relativePath at the destination.
	Download(ctx context.Context, relativePath string) (io.ReadCloser, error)

	// List lists items matching the given prefix.
	List(ctx context.Context, prefix string) ([]ItemInfo, error)

	// Delete removes the item at relativePath.
	Delete(ctx context.Context, relativePath string) error
}
