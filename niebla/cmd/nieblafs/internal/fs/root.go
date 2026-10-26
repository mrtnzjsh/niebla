package fs

import (
	"context"
	"os"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type Root struct{}

func (r *Root) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 1
	a.Mode = os.ModeDir | 0755
	a.Mtime = time.Now()
	return nil
}

func (r *Root) Lookup(ctx context.Context, name string) (fs.Node, error) {
	return nil, syscall.ENOENT
}

func (r *Root) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return nil, nil
}