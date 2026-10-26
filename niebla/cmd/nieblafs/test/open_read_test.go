package test

import (
	"context"
	"testing"

	"bazil.org/fuse"
	"niebla/cmd/nieblafs/internal/fs"
)

func TestFileOpenRead(t *testing.T) {
	file := &fs.File{Name: "testfile"}

	var attr fuse.Attr
	if err := file.Attr(context.Background(), &attr); err != nil {
		t.Fatalf("file.Attr failed: %v", err)
	}
	if attr.Mode != 0644 {
		t.Errorf("expected mode 0644, got %v", attr.Mode)
	}

	readReq := &fuse.ReadRequest{}
	readResp := &fuse.ReadResponse{}
	if err := file.Read(context.Background(), readReq, readResp); err != nil {
		t.Fatalf("file.Read failed: %v", err)
	}

	writeReq := &fuse.WriteRequest{Data: []byte("hello")}
	writeResp := &fuse.WriteResponse{}
	if err := file.Write(context.Background(), writeReq, writeResp); err != nil {
		t.Fatalf("file.Write failed: %v", err)
	}
	if writeResp.Size != 5 {
		t.Errorf("expected write size 5, got %d", writeResp.Size)
	}
}