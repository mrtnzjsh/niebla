package fs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"niebla/cmd/nieblafs/internal/metadata"
)

// FS represents the virtual Niebla filesystem backend.
type FS struct {
	ScratchDir string
	CacheDir   string
	Cache      *metadata.MetadataCache
}

// NewFS constructs an initialized FS instance.
func NewFS(scratchDir, cacheDir string, cache *metadata.MetadataCache) *FS {
	if cache == nil {
		cache = metadata.NewMetadataCache()
	}
	return &FS{
		ScratchDir: scratchDir,
		CacheDir:   cacheDir,
		Cache:      cache,
	}
}

// Root returns the root virtual directory node.
func (f *FS) Root() (fs.Node, error) {
	return &Dir{
		fs:    f,
		path:  "",
		inode: 1,
	}, nil
}

// Dir represents a directory node in the virtual filesystem.
type Dir struct {
	fs    *FS
	path  string
	inode uint64
}

// Root is an alias for the root Dir node to preserve API compatibility.
type Root = Dir

var (
	_ fs.Node               = (*Dir)(nil)
	_ fs.NodeStringLookuper = (*Dir)(nil)
	_ fs.HandleReadDirAller = (*Dir)(nil)
)

// Attr populates directory attributes.
func (d *Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = d.inode
	if a.Inode == 0 {
		a.Inode = 1
	}
	a.Mode = os.ModeDir | 0755
	a.Mtime = time.Now()

	if d.fs != nil && d.fs.ScratchDir != "" {
		scratchDir := filepath.Join(d.fs.ScratchDir, d.path)
		if fi, err := os.Stat(scratchDir); err == nil {
			a.Mtime = fi.ModTime()
			a.Mode = fi.Mode()
		}
	}
	return nil
}

// Lookup finds a child node by name, prioritizing local CoW scratch overlay before CAS blobs.
func (d *Dir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var relPath string
	if d.path == "" {
		relPath = name
	} else {
		relPath = filepath.Join(d.path, name)
	}

	// 1. Check Scratch Directory (Copy-on-Write overlay takes precedence)
	if d.fs != nil && d.fs.ScratchDir != "" {
		scratchPath := filepath.Join(d.fs.ScratchDir, relPath)
		if fi, err := os.Lstat(scratchPath); err == nil {
			if fi.IsDir() {
				return &Dir{
					fs:    d.fs,
					path:  relPath,
					inode: hashPathToInode(relPath),
				}, nil
			}
			return &File{
				fs:          d.fs,
				path:        relPath,
				Name:        name,
				scratchPath: scratchPath,
				isScratch:   true,
				size:        fi.Size(),
				mode:        uint32(fi.Mode()),
				inode:       hashPathToInode(relPath),
			}, nil
		}
	}

	// 2. Check In-Memory Metadata Cache (base Merkle tree entries)
	if d.fs != nil && d.fs.Cache != nil {
		if meta, ok := d.fs.Cache.Get(relPath); ok {
			if meta.SubTreeHash != "" || (meta.Mode&syscall.S_IFDIR != 0) {
				return &Dir{
					fs:    d.fs,
					path:  relPath,
					inode: hashPathToInode(relPath),
				}, nil
			}
			blobPath := ""
			if d.fs.CacheDir != "" && meta.BlobSHA256 != "" {
				blobPath = filepath.Join(d.fs.CacheDir, meta.BlobSHA256)
			}
			return &File{
				fs:        d.fs,
				path:      relPath,
				Name:      name,
				blobPath:  blobPath,
				blobHash:  meta.BlobSHA256,
				size:      meta.ByteSize,
				mode:      meta.Mode,
				isScratch: false,
				inode:     hashPathToInode(relPath),
			}, nil
		}
	}

	return nil, syscall.ENOENT
}

// ReadDirAll streams all directory entries combining CoW scratch and metadata cache.
func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	seen := make(map[string]bool)
	var entries []fuse.Dirent

	// 1. Read entries from local scratch directory
	if d.fs != nil && d.fs.ScratchDir != "" {
		scratchDir := filepath.Join(d.fs.ScratchDir, d.path)
		if des, err := os.ReadDir(scratchDir); err == nil {
			for _, de := range des {
				seen[de.Name()] = true
				t := fuse.DT_File
				if de.IsDir() {
					t = fuse.DT_Dir
				}
				entries = append(entries, fuse.Dirent{
					Name: de.Name(),
					Type: t,
				})
			}
		}
	}

	// 2. Read entries from metadata cache
	if d.fs != nil && d.fs.Cache != nil {
		cachedMetas := d.fs.Cache.ListDir(d.path)
		for _, meta := range cachedMetas {
			entryName := filepath.Base(meta.Name)
			if seen[entryName] {
				continue
			}
			seen[entryName] = true
			t := fuse.DT_File
			if meta.SubTreeHash != "" || (meta.Mode&syscall.S_IFDIR != 0) {
				t = fuse.DT_Dir
			}
			entries = append(entries, fuse.Dirent{
				Name: entryName,
				Type: t,
			})
		}
	}

	return entries, nil
}

func hashPathToInode(path string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(path); i++ {
		h ^= uint64(path[i])
		h *= 1099511628211
	}
	if h <= 1 {
		h = 2
	}
	return h
}
