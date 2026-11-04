package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"niebla/cmd/nieblafs/internal/metadata"
	"niebla/cmd/nieblafs/internal/remote"
	"niebla/pkg/cas"
	"niebla/pkg/tree"
)

// FS represents the virtual Niebla filesystem backend.
type FS struct {
	ScratchDir   string
	CacheDir     string
	Cache        *metadata.MetadataCache
	CASStorage   *cas.Storage
	RemoteClient remote.Client
	Fetcher      *remote.BlobFetcher
}

// Option configures optional FS dependencies.
type Option func(*FS)

// WithRemoteClient binds a remote gRPC client to FS.
func WithRemoteClient(rc remote.Client) Option {
	return func(f *FS) {
		f.RemoteClient = rc
	}
}

// WithFetcher binds a BlobFetcher to FS.
func WithFetcher(bf *remote.BlobFetcher) Option {
	return func(f *FS) {
		f.Fetcher = bf
	}
}

// WithCASStorage binds a local CAS Storage instance to FS.
func WithCASStorage(cs *cas.Storage) Option {
	return func(f *FS) {
		f.CASStorage = cs
	}
}

// NewFS constructs an initialized FS instance with optional remote capabilities.
func NewFS(scratchDir, cacheDir string, cache *metadata.MetadataCache, opts ...Option) *FS {
	if cache == nil {
		cache = metadata.NewMetadataCache()
	}
	f := &FS{
		ScratchDir: scratchDir,
		CacheDir:   cacheDir,
		Cache:      cache,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// EnsureDirectoryLoaded fetches directory entries lazily from the remote server if not yet cached, using paged streaming.
func (f *FS) EnsureDirectoryLoaded(ctx context.Context, dir string) error {
	if f.Cache == nil || f.Cache.IsDirectoryLoaded(dir) {
		return nil
	}
	if f.RemoteClient == nil {
		return nil
	}

	subTreeHash, ok := f.Cache.GetSubtreeHash(dir)
	if !ok || len(subTreeHash) == 0 {
		return nil
	}

	err := f.RemoteClient.FetchTreePaged(ctx, subTreeHash, 5000, func(batch []tree.Entry) error {
		f.Cache.PopulateDir(dir, batch)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to fetch tree entries for %q: %w", dir, err)
	}

	return nil
}

// ResolveBlobPath returns the expected local path for a cached CAS blob.
func (f *FS) ResolveBlobPath(sha256Hex string) string {
	if f.CASStorage != nil && sha256Hex != "" {
		if p, err := f.CASStorage.BlobPath(sha256Hex); err == nil {
			return p
		}
	}
	if f.CacheDir != "" && sha256Hex != "" {
		return filepath.Join(f.CacheDir, sha256Hex)
	}
	return ""
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

	// 1. Check Negative Lookup Cache first to suppress redundant remote calls
	if d.fs != nil && d.fs.Cache != nil && d.fs.Cache.IsNegative(relPath) {
		return nil, syscall.ENOENT
	}

	// 2. Check Scratch Directory (Copy-on-Write overlay takes precedence)
	if d.fs != nil && d.fs.ScratchDir != "" {
		scratchPath := filepath.Join(d.fs.ScratchDir, relPath)
		if fi, err := os.Lstat(scratchPath); err == nil {
			if d.fs.Cache != nil {
				d.fs.Cache.ClearNegative(relPath)
			}
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

	// 3. Ensure parent directory entries are loaded from remote server if needed
	if d.fs != nil {
		_ = d.fs.EnsureDirectoryLoaded(ctx, d.path)
	}

	// 4. Check In-Memory Metadata Cache (base Merkle tree entries)
	if d.fs != nil && d.fs.Cache != nil {
		if meta, ok := d.fs.Cache.Get(relPath); ok {
			if meta.SubTreeHash != "" || (meta.Mode&syscall.S_IFDIR != 0) {
				return &Dir{
					fs:    d.fs,
					path:  relPath,
					inode: hashPathToInode(relPath),
				}, nil
			}
			blobPath := d.fs.ResolveBlobPath(meta.BlobSHA256)
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
		// Negative hit: record missing entry to suppress redundant lookups
		d.fs.Cache.SetNegative(relPath, 5*time.Second)
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

	// 2. Ensure parent directory entries are loaded from remote server if needed
	if d.fs != nil {
		_ = d.fs.EnsureDirectoryLoaded(ctx, d.path)
	}

	// 3. Read entries from metadata cache
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
