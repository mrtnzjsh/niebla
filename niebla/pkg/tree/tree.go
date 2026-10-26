package tree

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	nieblav1 "niebla/pkg/proto/nieblav1"
)

// EmptyTreeHash is the SHA-256 hash of an empty tree (empty byte slice).
var EmptyTreeHash = []byte{
	0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
	0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
	0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
	0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
}

// Entry represents an item in a directory tree (either a file or a subdirectory).
type Entry struct {
	Name        string
	Mode        uint32
	Size        uint64
	BlobSHA256  []byte // 32-byte blob hash for files
	SubTreeHash []byte // 32-byte tree hash for subdirectories
}

// IsDir returns true if this entry represents a subdirectory.
func (e *Entry) IsDir() bool {
	return len(e.SubTreeHash) > 0
}

// ToProto converts an Entry to a protobuf NodeEntry.
func (e *Entry) ToProto() *nieblav1.NodeEntry {
	return &nieblav1.NodeEntry{
		Name:        e.Name,
		Mode:        e.Mode,
		Size:        e.Size,
		BlobSha256:  e.BlobSHA256,
		SubTreeHash: e.SubTreeHash,
	}
}

// FromProto creates an Entry from a protobuf NodeEntry.
func FromProto(p *nieblav1.NodeEntry) Entry {
	if p == nil {
		return Entry{}
	}
	return Entry{
		Name:        p.Name,
		Mode:        p.Mode,
		Size:        p.Size,
		BlobSHA256:  p.BlobSha256,
		SubTreeHash: p.SubTreeHash,
	}
}

// SortEntries sorts entries in-place lexicographically by Name.
func SortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
}

// ComputeTreeHash calculates the deterministic Merkle tree hash for a set of entries.
// If entries is empty, it returns the standard EmptyTreeHash.
func ComputeTreeHash(entries []Entry) []byte {
	if len(entries) == 0 {
		return append([]byte(nil), EmptyTreeHash...)
	}

	// Create a copy and sort deterministically by name
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	SortEntries(sorted)

	var buf bytes.Buffer
	for _, e := range sorted {
		// 1. Name: uint16 length + utf-8 bytes
		nameBytes := []byte(e.Name)
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(nameBytes)))
		buf.Write(nameBytes)

		// 2. Mode: uint32
		_ = binary.Write(&buf, binary.BigEndian, e.Mode)

		// 3. Size: uint64
		_ = binary.Write(&buf, binary.BigEndian, e.Size)

		// 4. BlobSHA256: 32 bytes (zeros if not a file)
		if len(e.BlobSHA256) == 32 {
			buf.Write(e.BlobSHA256)
		} else {
			buf.Write(make([]byte, 32))
		}

		// 5. SubTreeHash: 32 bytes (zeros if leaf file)
		if len(e.SubTreeHash) == 32 {
			buf.Write(e.SubTreeHash)
		} else {
			buf.Write(make([]byte, 32))
		}
	}

	h := sha256.Sum256(buf.Bytes())
	return h[:]
}

// FindEntry performs a binary search for an entry with the given name in sorted entries.
func FindEntry(entries []Entry, name string) (*Entry, bool) {
	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].Name >= name
	})
	if idx < len(entries) && entries[idx].Name == name {
		return &entries[idx], true
	}
	return nil, false
}

// FetchTreeFunc is a callback function for fetching directory entries for a given tree hash.
type FetchTreeFunc func(treeHash []byte) ([]Entry, error)

// ResolvePath navigates through subtrees starting from rootTreeHash to find the entry at path.
// Path segments are separated by "/".
func ResolvePath(rootTreeHash []byte, path string, fetchTree FetchTreeFunc) (*Entry, error) {
	cleaned := strings.Trim(filepathClean(path), "/")
	if cleaned == "" || cleaned == "." {
		// Root directory itself
		return &Entry{
			Name:        "",
			Mode:        0040755,
			SubTreeHash: rootTreeHash,
		}, nil
	}

	segments := strings.Split(cleaned, "/")
	currentTreeHash := rootTreeHash

	for i, segment := range segments {
		entries, err := fetchTree(currentTreeHash)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tree %s at segment %q: %w", hex.EncodeToString(currentTreeHash), segment, err)
		}

		entry, found := FindEntry(entries, segment)
		if !found {
			// Try non-sorted fallback if entries were unsorted
			for idx := range entries {
				if entries[idx].Name == segment {
					entry = &entries[idx]
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("entry %q not found in tree %s", segment, hex.EncodeToString(currentTreeHash))
			}
		}

		isLast := (i == len(segments)-1)
		if isLast {
			return entry, nil
		}

		if !entry.IsDir() {
			return nil, fmt.Errorf("segment %q is a file, cannot traverse into it", segment)
		}

		currentTreeHash = entry.SubTreeHash
	}

	return nil, errors.New("failed to resolve path")
}

func filepathClean(p string) string {
	parts := strings.Split(p, "/")
	var out []string
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}
