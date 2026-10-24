package fs

import (
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"os"
)

// CreateOperation handles the create operation
func (r *root) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, fuse.Status) {
	overlayPath := "/path/to/overlay/" + req.Name
	file, err := os.OpenFile(overlayPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, req.Mode)
	if err != nil {
		return nil, nil, fuse.EIO
	}
	defer file.Close()

	f := &File{Name: req.Name}
	return f, f, fuse.OK
}
