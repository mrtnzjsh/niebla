package grpc

import (
	"strconv"
	"testing"

	nieblav1 "niebla/pkg/proto/nieblav1"
)

func makeTestEntries(n int) []*nieblav1.NodeEntry {
	entries := make([]*nieblav1.NodeEntry, n)
	for i := 0; i < n; i++ {
		entries[i] = &nieblav1.NodeEntry{
			Name: "file_" + strconv.Itoa(i),
			Size: uint64(i * 10),
		}
	}
	return entries
}

func TestPaginateTreeEntries_All(t *testing.T) {
	entries := makeTestEntries(12)

	t.Run("backward_compatible_all", func(t *testing.T) {
		resp, err := paginateTreeEntries(entries, 0, "")
		if err != nil || len(resp.Entries) != 12 || resp.NextPageToken != "" {
			t.Fatalf("unexpected result: err=%v, count=%d, token=%s", err, len(resp.Entries), resp.NextPageToken)
		}
	})

	t.Run("first_page", func(t *testing.T) {
		resp, err := paginateTreeEntries(entries, 5, "")
		if err != nil || len(resp.Entries) != 5 || resp.NextPageToken != "5" {
			t.Fatalf("unexpected result: err=%v, count=%d, token=%s", err, len(resp.Entries), resp.NextPageToken)
		}
	})

	t.Run("second_page", func(t *testing.T) {
		resp, err := paginateTreeEntries(entries, 5, "5")
		if err != nil || len(resp.Entries) != 5 || resp.NextPageToken != "10" {
			t.Fatalf("unexpected result: err=%v, count=%d, token=%s", err, len(resp.Entries), resp.NextPageToken)
		}
	})

	t.Run("final_page", func(t *testing.T) {
		resp, err := paginateTreeEntries(entries, 5, "10")
		if err != nil || len(resp.Entries) != 2 || resp.NextPageToken != "" {
			t.Fatalf("unexpected result: err=%v, count=%d, token=%s", err, len(resp.Entries), resp.NextPageToken)
		}
	})

	t.Run("out_of_bounds", func(t *testing.T) {
		resp, err := paginateTreeEntries(entries, 5, "50")
		if err != nil || len(resp.Entries) != 0 || resp.NextPageToken != "" {
			t.Fatalf("unexpected result: err=%v, count=%d, token=%s", err, len(resp.Entries), resp.NextPageToken)
		}
	})

	t.Run("invalid_token", func(t *testing.T) {
		if _, err := paginateTreeEntries(entries, 5, "invalid-token"); err == nil {
			t.Fatalf("expected error for invalid page_token, got nil")
		}
	})
}
