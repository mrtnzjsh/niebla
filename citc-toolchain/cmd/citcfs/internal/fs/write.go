package fs

import (
	"github.com/bazil/fuse"
	"github.com/bazil/fuse/fs"
	"io"
	"os"
)

// WriteOperation handles the write operation
func (f *File) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	overlayPath := f.getOverlayPath(req.Offset)
	if _, err := os.Stat(overlayPath); os.IsNotExist(err) {
		os.MkdirAll(overlayPath, os.ModePerm)
	}

	file, err := os.OpenFile(overlayPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Seek(req.Offset, 0)
	if err != nil {
		return err
	}

	written, err := io.CopyN(file, req.Data, int64(req.Size))
	if err != nil {
		return err
	}

	resp.Size = uint32(written)
	return nil
}

// getOverlayPath constructs the path for the overlay file
func (f *File) getOverlayPath(offset int64) string {
	return "/path/to/overlay/" + f.Name + ".overlay"
}
