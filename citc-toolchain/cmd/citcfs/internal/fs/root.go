package fs

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"google.golang.org/grpc"
	pb "citc-toolchain/proto/v1"
)

type root struct{}

func (r *root) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 1
	a.Mode = os.ModeDir | 0755
	a.Mtime = time.Now()
	return nil
}

func (r *root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Node, fuse.Status) {
	// Placeholder for lookup
	return nil, fuse.ENOENT
}

func (r *root) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	// Placeholder for readdir
	return nil, nil
}

type file struct {
	name string
	conn *grpc.ClientConn
}

func (f *file) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 2
	a.Mode = 0644
	a.Size = uint64(len(f.name))
	a.Mtime = time.Now()
	return nil
}

func (f *file) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	cachePath := fmt.Sprintf("/tmp/cache/%s", f.name)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		client := pb.NewYourServiceClient(f.conn)
		fileData, err := client.ReadFile(ctx, &pb.ReadFileRequest{Name: f.name})
		if err != nil {
			return fuse.EIO
		}
		err = ioutil.WriteFile(cachePath, fileData.Data, 0644)
		if err != nil {
			return fuse.EIO
		}
		resp.Data = fileData.Data
	} else {
		data, err := ioutil.ReadFile(cachePath)
		if err != nil {
			return fuse.EIO
		}
		resp.Data = data
	}
	return nil
}

func (r *root) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, fuse.Status) {
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	cachePath := fmt.Sprintf("/tmp/cache/%s", req.Name)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		client := pb.NewYourServiceClient(conn)
		fileData, err := client.ReadFile(ctx, &pb.ReadFileRequest{Name: req.Name})
		if err != nil {
			return nil, fuse.EIO
		}
		err = ioutil.WriteFile(cachePath, fileData.Data, 0644)
		if err != nil {
			return nil, fuse.EIO
		}
	}
	return &file{name: req.Name, conn: conn}, nil
}