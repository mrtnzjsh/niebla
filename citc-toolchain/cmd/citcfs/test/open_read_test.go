package test

import (
	"context"
	"testing"

	"github.com/bazil/fuse"
	"github.com/bazil/fuse/fs"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	pb "citc-toolchain/proto/v1"
	"citc-toolchain/cmd/citcfs/internal/fs"
)

type MockYourServiceClient struct {
	mock.Mock
}

func (m *MockYourServiceClient) ReadFile(ctx context.Context, in *pb.ReadFileRequest, opts ...grpc.CallOption) (*pb.ReadFileResponse, error) {
	args := m.Called(ctx, in)
	return args.Get(0).(*pb.ReadFileResponse), args.Error(1)
}

func TestOpen(t *testing.T) {
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		t.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	mockClient := new(MockYourServiceClient)
	mockClient.On("ReadFile", mock.Anything, &pb.ReadFileRequest{Name: "testfile"}).Return(&pb.ReadFileResponse{Data: []byte("test data")}, nil)

	root := &fs.Root{}
	req := &fuse.OpenRequest{Name: "testfile"}
	resp := &fuse.OpenResponse{}

	handle, status := root.Open(context.Background(), req, resp)
	if status != fuse.OK {
		t.Errorf("Expected OK, got %v", status)
	}

	f, ok := handle.(*fs.File)
	if !ok {
		t.Errorf("Expected *fs.File, got %T", handle)
	}

	if f.name != "testfile" {
		t.Errorf("Expected name 'testfile', got '%s'", f.name)
	}
}

func TestRead(t *testing.T) {
	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		t.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	mockClient := new(MockYourServiceClient)
	mockClient.On("ReadFile", mock.Anything, &pb.ReadFileRequest{Name: "testfile"}).Return(&pb.ReadFileResponse{Data: []byte("test data")}, nil)

	root := &fs.Root{}
	req := &fuse.OpenRequest{Name: "testfile"}
	resp := &fuse.OpenResponse{}

	handle, status := root.Open(context.Background(), req, resp)
	if status != fuse.OK {
		t.Errorf("Expected OK, got %v", status)
	}

	f, ok := handle.(*fs.File)
	if !ok {
		t.Errorf("Expected *fs.File, got %T", handle)
	}

	readResp := &fuse.ReadResponse{}
	status = f.Read(context.Background(), &fuse.ReadRequest{}, readResp)
	if status != fuse.OK {
		t.Errorf("Expected OK, got %v", status)
	}

	if string(readResp.Data) != "test data" {
		t.Errorf("Expected 'test data', got '%s'", string(readResp.Data))
	}
}