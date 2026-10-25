package drivers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FilesystemConfig configures the filesystem storage driver.
type FilesystemConfig struct {
	RootPath      string
	BufferSizeMB  int
	AutoCreateDir bool
}

// FilesystemDriver stores backup bundles and blobs in a directory (local disk, ZFS mount, NFS/SMB).
type FilesystemDriver struct {
	rootPath   string
	bufferSize int
}

// NewFilesystemDriver creates an initialized FilesystemDriver.
func NewFilesystemDriver(cfg FilesystemConfig) (*FilesystemDriver, error) {
	if cfg.RootPath == "" {
		return nil, fmt.Errorf("root path cannot be empty")
	}

	absPath, err := filepath.Abs(cfg.RootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %s: %w", cfg.RootPath, err)
	}

	if cfg.AutoCreateDir {
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create root directory %s: %w", absPath, err)
		}
	}

	bufSize := cfg.BufferSizeMB * 1024 * 1024
	if bufSize <= 0 {
		bufSize = 8 * 1024 * 1024 // 8MB default for high-speed 10G throughput
	}

	return &FilesystemDriver{
		rootPath:   absPath,
		bufferSize: bufSize,
	}, nil
}

func (f *FilesystemDriver) Name() string {
	return "filesystem"
}

func (f *FilesystemDriver) targetPath(relativePath string) (string, error) {
	cleanRel := filepath.Clean(relativePath)
	if strings.HasPrefix(cleanRel, "..") {
		return "", fmt.Errorf("path traversal attack detected: %s", relativePath)
	}
	return filepath.Join(f.rootPath, cleanRel), nil
}

// Upload writes a stream to a temporary file and atomically renames it to relativePath.
func (f *FilesystemDriver) Upload(ctx context.Context, relativePath string, r io.Reader, size int64) error {
	destPath, err := f.targetPath(relativePath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary upload file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()

	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	buf := make([]byte, f.bufferSize)
	if _, err := io.CopyBuffer(tmpFile, r, buf); err != nil {
		return fmt.Errorf("failed to write upload stream: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync upload file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close upload file: %w", err)
	}

	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("failed to atomically commit upload file to %s: %w", destPath, err)
	}

	return nil
}

// Download opens and returns the file at relativePath.
func (f *FilesystemDriver) Download(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	destPath, err := f.targetPath(relativePath)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", destPath, err)
	}

	return file, nil
}

// List scans files matching the prefix under rootPath.
func (f *FilesystemDriver) List(ctx context.Context, prefix string) ([]ItemInfo, error) {
	searchDir, err := f.targetPath(prefix)
	if err != nil {
		return nil, err
	}

	var items []ItemInfo
	// If searchDir doesn't exist, return empty list
	if _, err := os.Stat(searchDir); os.IsNotExist(err) {
		// Try parent if prefix includes filename pattern
		searchDir = filepath.Dir(searchDir)
		if _, err := os.Stat(searchDir); os.IsNotExist(err) {
			return nil, nil
		}
	}

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(f.rootPath, path)
		if err != nil {
			return err
		}
		if prefix == "" || strings.HasPrefix(rel, prefix) {
			items = append(items, ItemInfo{
				Name:    rel,
				Size:    info.Size(),
				ModTime: info.ModTime().UTC(),
			})
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	return items, nil
}

// Delete removes the file at relativePath.
func (f *FilesystemDriver) Delete(ctx context.Context, relativePath string) error {
	destPath, err := f.targetPath(relativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file %s: %w", destPath, err)
	}
	return nil
}
