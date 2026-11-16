package fs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var (
	_ fs.Node          = (*File)(nil)
	_ fs.HandleReader  = (*File)(nil)
	_ fs.HandleWriter  = (*File)(nil)
	_ fs.NodeSetattrer = (*File)(nil)
)

// File represents a file node backed by either local CoW scratch storage or a CAS blob.
type File struct {
	fs          *FS
	path        string
	Name        string
	scratchPath string
	blobPath    string
	blobHash    string
	size        int64
	mode        uint32
	isScratch   bool
	inode       uint64
}

// Attr populates file attributes.
func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = f.inode
	if a.Inode == 0 {
		a.Inode = 2
	}
	a.Mtime = time.Now()
	a.Mode = 0644
	a.Uid = uint32(os.Geteuid())
	a.Gid = uint32(os.Getegid())
	if f.mode != 0 {
		a.Mode = os.FileMode(f.mode)
	}

	if f.isScratch && f.scratchPath != "" {
		if fi, err := os.Stat(f.scratchPath); err == nil {
			a.Size = uint64(fi.Size())
			a.Mode = fi.Mode()
			a.Mtime = fi.ModTime()
			if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
				a.Uid = stat.Uid
				a.Gid = stat.Gid
			}
			return nil
		}
	}

	if f.size > 0 {
		a.Size = uint64(f.size)
	} else if f.Name != "" && f.blobPath == "" && f.scratchPath == "" {
		a.Size = uint64(len(f.Name))
	}
	return nil
}

// Read reads file contents from scratch or cached CAS blob at the requested offset.
func (f *File) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	if f.scratchPath != "" {
		return readFromFileAt(f.scratchPath, req.Offset, req.Size, resp)
	}

	if f.blobPath != "" {
		if fileExists(f.blobPath) {
			return readFromFileAt(f.blobPath, req.Offset, req.Size, resp)
		}
	}

	if f.fs != nil && f.fs.Fetcher != nil && f.blobHash != "" {
		cachedPath, err := f.fs.Fetcher.EnsureBlob(ctx, f.blobHash)
		if err == nil && cachedPath != "" {
			f.blobPath = cachedPath
			return readFromFileAt(f.blobPath, req.Offset, req.Size, resp)
		}
	}

	resp.Data = []byte{}
	return nil
}

func (f *File) ensureScratchPromoted(ctx context.Context) error {
	if f.isScratch || f.fs == nil || f.fs.ScratchDir == "" {
		return nil
	}

	targetPath := filepath.Join(f.fs.ScratchDir, f.path)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	// If base blob is not yet cached locally, stream it first so scratch gets the full base content
	if (f.blobPath == "" || !fileExists(f.blobPath)) && f.fs.Fetcher != nil && f.blobHash != "" {
		if cachedPath, err := f.fs.Fetcher.EnsureBlob(ctx, f.blobHash); err == nil {
			f.blobPath = cachedPath
		}
	}

	// If base blob exists, copy it first to initialize scratch file using streaming I/O
	if f.blobPath != "" && fileExists(f.blobPath) {
		mode := os.FileMode(0644)
		if f.mode != 0 {
			mode = os.FileMode(f.mode)
		}
		if src, err := os.Open(f.blobPath); err == nil {
			if dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode); err == nil {
				buf := make([]byte, 1024*1024) // 1MB streaming buffer
				_, _ = io.CopyBuffer(dst, src, buf)
				_ = dst.Close()
			}
			_ = src.Close()
		}
	}

	f.scratchPath = targetPath
	f.isScratch = true
	if f.fs != nil && f.fs.Cache != nil {
		f.fs.Cache.ClearNegative(f.path)
	}
	return nil
}

// Setattr handles truncation (ftruncate/O_TRUNC) and metadata attribute changes.
func (f *File) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	if err := f.ensureScratchPromoted(ctx); err != nil {
		return err
	}
	if f.scratchPath == "" {
		return f.Attr(ctx, &resp.Attr)
	}

	if req.Valid.Size() {
		if err := os.Truncate(f.scratchPath, int64(req.Size)); err != nil {
			return err
		}
		f.size = int64(req.Size)
	}
	if req.Valid.Mode() {
		if err := os.Chmod(f.scratchPath, req.Mode); err != nil {
			return err
		}
		f.mode = uint32(req.Mode)
	}

	return f.Attr(ctx, &resp.Attr)
}

// Write writes data to the file, promoting it to the local CoW scratch overlay if needed.
func (f *File) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	if err := f.ensureScratchPromoted(ctx); err != nil {
		return err
	}
	if f.scratchPath == "" {
		resp.Size = len(req.Data)
		return nil
	}

	file, err := os.OpenFile(f.scratchPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	n, err := file.WriteAt(req.Data, req.Offset)
	if err != nil {
		return err
	}
	resp.Size = n
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func readFromFileAt(filePath string, offset int64, size int, resp *fuse.ReadResponse) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, size)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return err
	}
	resp.Data = buf[:n]
	return nil
}
