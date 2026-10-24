package fs

import (
	"github.com/bazil/fuse"
	"github.com/bazil/fuse/fs"
)

type file struct {
	name string
}

func (f *file) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 2
	a.Mode = 0644
	a.Mtime = time.Now()
	a.Size = 1024
	return nil
}

func (f *file) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	resp.Data = make([]byte, req.Size)
	return nil
}