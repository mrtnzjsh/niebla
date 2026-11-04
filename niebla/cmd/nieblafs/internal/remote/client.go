package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"

	"niebla/internal/cli"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"
)

// Client provides remote access to trees and blobs on niebla-server.
type Client interface {
	FetchTree(ctx context.Context, treeHash []byte) ([]tree.Entry, error)
	FetchTreePaged(ctx context.Context, treeHash []byte, pageSize int32, onPage func([]tree.Entry) error) error
	StreamBlob(ctx context.Context, sha256Bytes []byte, dst io.Writer) error
	Close() error
}

type grpcClient struct {
	conn   *grpc.ClientConn
	client nieblav1.NieblaServiceClient
}

// NewClient dials the niebla-server at serverAddr using standard client credentials.
func NewClient(ctx context.Context, serverAddr string) (Client, error) {
	conn, client, err := cli.DialGRPC(ctx, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("nieblafs failed to dial server %s: %w", serverAddr, err)
	}
	return &grpcClient{conn: conn, client: client}, nil
}

// NewClientWithServiceClient wraps an existing service client (useful for unit tests).
func NewClientWithServiceClient(client nieblav1.NieblaServiceClient) Client {
	return &grpcClient{client: client}
}

func (c *grpcClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// FetchTreePaged queries GetTree with pagination, invoking onPage for each batch of entries received.
func (c *grpcClient) FetchTreePaged(ctx context.Context, treeHash []byte, pageSize int32, onPage func([]tree.Entry) error) error {
	if len(treeHash) == 0 || bytes.Equal(treeHash, tree.EmptyTreeHash) {
		return nil
	}

	if pageSize <= 0 {
		pageSize = 5000
	}

	var pageToken string
	for {
		resp, err := c.client.GetTree(ctx, &nieblav1.GetTreeRequest{
			TreeHash:  treeHash,
			PageSize:  pageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch tree %x: %w", treeHash, err)
		}

		batch := make([]tree.Entry, len(resp.Entries))
		for i, protoEntry := range resp.Entries {
			batch[i] = tree.FromProto(protoEntry)
		}

		if err := onPage(batch); err != nil {
			return err
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return nil
}

// FetchTree queries GetTree for a given Merkle tree hash, following pagination tokens until all pages are assembled.
func (c *grpcClient) FetchTree(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	var allEntries []tree.Entry
	err := c.FetchTreePaged(ctx, treeHash, 5000, func(batch []tree.Entry) error {
		allEntries = append(allEntries, batch...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allEntries, nil
}

// StreamBlob streams blob chunks via GetBlob and writes them into dst, verifying SHA-256 integrity.
func (c *grpcClient) StreamBlob(ctx context.Context, sha256Bytes []byte, dst io.Writer) error {
	if len(sha256Bytes) != 32 {
		return fmt.Errorf("invalid sha256 byte length: expected 32, got %d", len(sha256Bytes))
	}

	stream, err := c.client.GetBlob(ctx, &nieblav1.GetBlobRequest{
		Sha256: sha256Bytes,
	})
	if err != nil {
		return fmt.Errorf("failed to open GetBlob stream for %x: %w", sha256Bytes, err)
	}

	hasher := sha256.New()
	multiWriter := io.MultiWriter(dst, hasher)

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading blob stream chunk for %x: %w", sha256Bytes, err)
		}
		if len(chunk.Data) > 0 {
			if _, err := multiWriter.Write(chunk.Data); err != nil {
				return fmt.Errorf("error writing blob chunk to destination: %w", err)
			}
		}
	}

	computed := hasher.Sum(nil)
	if !bytes.Equal(computed, sha256Bytes) {
		return fmt.Errorf("blob checksum mismatch for %x: computed %x", sha256Bytes, computed)
	}

	return nil
}

