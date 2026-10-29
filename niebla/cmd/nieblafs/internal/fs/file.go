package fs

import (
	"context"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var (
	_ fs.Node         = (*File)(nil)
	_ fs.HandleReader = (*File)(nil)
	_ fs.HandleWriter = (*File)(nil)
)

type File struct {
	Name string
}

func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 2
	a.Mode = 0644
	a.Mtime = time.Now()
	a.Size = uint64(len(f.Name))
	return nil
}

func (f *File) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	resp.Data = []byte{}
	return nil
}

func (f *File) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	resp.Size = len(req.Data)
	return nil
}
