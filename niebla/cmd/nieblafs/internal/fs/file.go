package fs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var (
	_ fs.Node         = (*File)(nil)
	_ fs.HandleReader = (*File)(nil)
	_ fs.HandleWriter = (*File)(nil)
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
	if f.mode != 0 {
		a.Mode = os.FileMode(f.mode)
	}

	if f.isScratch && f.scratchPath != "" {
		if fi, err := os.Stat(f.scratchPath); err == nil {
			a.Size = uint64(fi.Size())
			a.Mode = fi.Mode()
			a.Mtime = fi.ModTime()
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
	// 1. If scratch path is populated, read directly from scratch
	if f.scratchPath != "" {
		return readFromFileAt(f.scratchPath, req.Offset, req.Size, resp)
	}

	// 2. If blobPath exists on disk, read from local CAS blob
	if f.blobPath != "" {
		if _, err := os.Stat(f.blobPath); err == nil {
			return readFromFileAt(f.blobPath, req.Offset, req.Size, resp)
		}
	}

	resp.Data = []byte{}
	return nil
}

// Write writes data to the file, promoting it to the local CoW scratch overlay if needed.
func (f *File) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	// If writing to virtual CAS file, promote to scratch copy-on-write
	if !f.isScratch && f.fs != nil && f.fs.ScratchDir != "" {
		targetPath := filepath.Join(f.fs.ScratchDir, f.path)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		// If base blob exists, copy it first to initialize scratch file
		if f.blobPath != "" {
			if data, err := os.ReadFile(f.blobPath); err == nil {
				_ = os.WriteFile(targetPath, data, 0644)
			}
		}
		f.scratchPath = targetPath
		f.isScratch = true
	}

	if f.scratchPath != "" {
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

	resp.Size = len(req.Data)
	return nil
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
