package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// DefaultChunkSize is the buffer size (64KB) used for streaming blob data.
	DefaultChunkSize = 64 * 1024
	tmpSubDir        = ".tmp"
)

// Storage manages local CAS blob storage partitioned by SHA-256 hash.
type Storage struct {
	baseDir string
	tmpDir  string
}

// NewStorage initializes a new CAS storage engine at baseDir.
func NewStorage(baseDir string) (*Storage, error) {
	if baseDir == "" {
		return nil, errors.New("baseDir cannot be empty")
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path of baseDir: %w", err)
	}

	tmpDir := filepath.Join(absBase, tmpSubDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create CAS tmp directory %s: %w", tmpDir, err)
	}

	return &Storage{
		baseDir: absBase,
		tmpDir:  tmpDir,
	}, nil
}

func (s *Storage) BaseDir() string {
	return s.baseDir
}

// RelativeBlobPath returns the portable relative partitioned key for a given SHA-256 hex string:
// ab/cd/abcdef...
func RelativeBlobPath(sha256Hex string) (string, error) {
	if len(sha256Hex) < 4 {
		return "", fmt.Errorf("invalid sha256 hex string %q: too short", sha256Hex)
	}
	part1 := sha256Hex[0:2]
	part2 := sha256Hex[2:4]
	return filepath.ToSlash(filepath.Join(part1, part2, sha256Hex)), nil
}

// ResolveStoragePath resolves a portable relative storage key (or legacy absolute path)
// against the Storage baseDir.
func (s *Storage) ResolveStoragePath(storageKey string) string {
	if storageKey == "" {
		return ""
	}
	fromSlash := filepath.FromSlash(storageKey)
	if filepath.IsAbs(fromSlash) {
		return fromSlash
	}
	return filepath.Join(s.baseDir, fromSlash)
}

// BlobPath returns the partitioned file path for a given SHA-256 hex string:
// ${BLOB_STORAGE_PATH}/ab/cd/abcdef...
func (s *Storage) BlobPath(sha256Hex string) (string, error) {
	rel, err := RelativeBlobPath(sha256Hex)
	if err != nil {
		return "", err
	}
	return s.ResolveStoragePath(rel), nil
}

// Exists checks if the blob with the given sha256 exists in storage.
func (s *Storage) Exists(sha256Bytes []byte) bool {
	hexHash := hex.EncodeToString(sha256Bytes)
	p, err := s.BlobPath(hexHash)
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// OpenBlob opens a blob for reading and returns the file handle and its size.
func (s *Storage) OpenBlob(sha256Bytes []byte) (*os.File, int64, error) {
	hexHash := hex.EncodeToString(sha256Bytes)
	targetPath, err := s.BlobPath(hexHash)
	if err != nil {
		return nil, 0, err
	}

	f, err := os.Open(targetPath)
	if err != nil {
		return nil, 0, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}

	return f, info.Size(), nil
}

// StreamBlob reads a slice of a blob specified by offset and length, sending chunks to sendFn.
// If length is 0, it reads until EOF.
func (s *Storage) StreamBlob(sha256Bytes []byte, offset, length uint64, chunkSize int, sendFn func([]byte) error) error {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}

	f, size, err := s.OpenBlob(sha256Bytes)
	if err != nil {
		return err
	}
	defer f.Close()

	if offset > uint64(size) {
		return io.EOF
	}

	if offset > 0 {
		if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to offset %d: %w", offset, err)
		}
	}

	var bytesToRead uint64
	if length == 0 {
		bytesToRead = uint64(size) - offset
	} else {
		bytesToRead = length
		if offset+bytesToRead > uint64(size) {
			bytesToRead = uint64(size) - offset
		}
	}

	buf := make([]byte, chunkSize)
	var totalRead uint64

	for totalRead < bytesToRead {
		limit := uint64(chunkSize)
		if remaining := bytesToRead - totalRead; remaining < limit {
			limit = remaining
		}

		n, rErr := io.ReadFull(f, buf[:limit])
		if n > 0 {
			totalRead += uint64(n)
			if err := sendFn(buf[:n]); err != nil {
				return err
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) || errors.Is(rErr, io.ErrUnexpectedEOF) {
				break
			}
			return rErr
		}
	}

	return nil
}

// BlobWriter handles streaming writes to a temporary file on the same filesystem,
// calculating SHA-256 simultaneously, and atomically moving it to the partitioned path.
type BlobWriter struct {
	storage   *Storage
	tmpFile   *os.File
	hasher    io.Writer
	hashState interface {
		Sum(b []byte) []byte
	}
	bytesWritten int64
	closed       bool
}

// BeginWriter initiates a streaming write to a temporary file in the CAS temp directory.
func (s *Storage) BeginWriter() (*BlobWriter, error) {
	tmpFile, err := os.CreateTemp(s.tmpDir, "blob-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary blob file: %w", err)
	}

	h := sha256.New()
	mw := io.MultiWriter(tmpFile, h)

	return &BlobWriter{
		storage:   s,
		tmpFile:   tmpFile,
		hasher:    mw,
		hashState: h,
	}, nil
}

// Write writes a slice of bytes to the temp file while updating the hash.
func (w *BlobWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("cannot write to closed BlobWriter")
	}
	n, err := w.hasher.Write(p)
	w.bytesWritten += int64(n)
	return n, err
}

// Commit finalizes the blob, atomically moves it to its hash-partitioned path,
// and returns the raw SHA-256 hash bytes, hex hash string, total byte size, and portable relative storage key.
func (w *BlobWriter) Commit() (hashBytes []byte, hexHash string, size int64, storagePath string, err error) {
	if w.closed {
		return nil, "", 0, "", errors.New("BlobWriter is already closed")
	}
	w.closed = true

	// Ensure temp file is fully flushed and closed before rename
	if syncErr := w.tmpFile.Sync(); syncErr != nil {
		_ = w.tmpFile.Close()
		_ = os.Remove(w.tmpFile.Name())
		return nil, "", 0, "", fmt.Errorf("failed to sync temp blob: %w", syncErr)
	}
	tmpName := w.tmpFile.Name()
	if closeErr := w.tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpName)
		return nil, "", 0, "", fmt.Errorf("failed to close temp blob: %w", closeErr)
	}

	hashBytes = w.hashState.Sum(nil)
	hexHash = hex.EncodeToString(hashBytes)
	size = w.bytesWritten

	relKey, err := RelativeBlobPath(hexHash)
	if err != nil {
		_ = os.Remove(tmpName)
		return nil, "", 0, "", err
	}
	targetPath := w.storage.ResolveStoragePath(relKey)

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		_ = os.Remove(tmpName)
		return nil, "", 0, "", fmt.Errorf("failed to create blob directory %s: %w", targetDir, err)
	}

	// Atomic rename within the same filesystem
	if err := os.Rename(tmpName, targetPath); err != nil {
		_ = os.Remove(tmpName)
		return nil, "", 0, "", fmt.Errorf("failed to atomically rename blob to %s: %w", targetPath, err)
	}

	return hashBytes, hexHash, size, relKey, nil
}

// Abort cancels the write and cleans up the temporary file.
func (w *BlobWriter) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	tmpName := w.tmpFile.Name()
	_ = w.tmpFile.Close()
	_ = os.Remove(tmpName)
}

// Put writes an entire reader's content into CAS storage atomically.
func (s *Storage) Put(reader io.Reader) (hashBytes []byte, hexHash string, size int64, storagePath string, err error) {
	writer, err := s.BeginWriter()
	if err != nil {
		return nil, "", 0, "", err
	}
	defer func() {
		if err != nil {
			writer.Abort()
		}
	}()

	buf := make([]byte, DefaultChunkSize)
	if _, err = io.CopyBuffer(writer, reader, buf); err != nil {
		return nil, "", 0, "", fmt.Errorf("failed to write blob content: %w", err)
	}

	return writer.Commit()
}
