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

	manifest, _ := MarshalManifest(entries)
	h := sha256.Sum256(manifest)
	return h[:]
}

// MarshalManifest serializes directory entries into a deterministic, sorted binary manifest.
// Notice that SHA256(MarshalManifest(entries)) == ComputeTreeHash(entries).
func MarshalManifest(entries []Entry) ([]byte, error) {
	if len(entries) == 0 {
		return []byte{}, nil
	}

	// Create a copy and sort deterministically by name
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	SortEntries(sorted)

	var buf bytes.Buffer
	for _, e := range sorted {
		nameBytes := []byte(e.Name)
		if len(nameBytes) > 0xffff {
			return nil, fmt.Errorf("entry name %q exceeds maximum uint16 length", e.Name)
		}

		// 1. Name: uint16 length + utf-8 bytes
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

	return buf.Bytes(), nil
}

// UnmarshalManifest deserializes a binary directory manifest into sorted Entry structs.
func UnmarshalManifest(data []byte) ([]Entry, error) {
	if len(data) == 0 {
		return []Entry{}, nil
	}

	r := bytes.NewReader(data)
	var entries []Entry
	var zeroHash [32]byte

	for r.Len() > 0 {
		var nameLen uint16
		if err := binary.Read(r, binary.BigEndian, &nameLen); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read entry name length: %w", err)
		}

		if r.Len() < int(nameLen)+4+8+32+32 {
			return nil, fmt.Errorf("corrupt manifest: truncated entry payload (need %d bytes, have %d)", int(nameLen)+76, r.Len())
		}

		nameBytes := make([]byte, nameLen)
		if _, err := r.Read(nameBytes); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read entry name: %w", err)
		}

		var mode uint32
		if err := binary.Read(r, binary.BigEndian, &mode); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read mode: %w", err)
		}

		var size uint64
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read size: %w", err)
		}

		blobHash := make([]byte, 32)
		if _, err := r.Read(blobHash); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read blob hash: %w", err)
		}
		if bytes.Equal(blobHash, zeroHash[:]) {
			blobHash = nil
		}

		subTreeHash := make([]byte, 32)
		if _, err := r.Read(subTreeHash); err != nil {
			return nil, fmt.Errorf("corrupt manifest: failed to read subtree hash: %w", err)
		}
		if bytes.Equal(subTreeHash, zeroHash[:]) {
			subTreeHash = nil
		}

		entries = append(entries, Entry{
			Name:        string(nameBytes),
			Mode:        mode,
			Size:        size,
			BlobSHA256:  blobHash,
			SubTreeHash: subTreeHash,
		})
	}

	return entries, nil
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

func fetchEntryMap(hash []byte, fetchTree FetchTreeFunc) (map[string]Entry, error) {
	entryMap := make(map[string]Entry)
	if len(hash) > 0 && !bytes.Equal(hash, EmptyTreeHash) {
		entries, err := fetchTree(hash)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			entryMap[e.Name] = e
		}
	}
	return entryMap, nil
}

func compareDiffEntry(relPath string, oldE, newE Entry, inOld, inNew bool, walk func(string, []byte, []byte) error, touched *[]string) error {
	if inOld && inNew {
		if oldE.IsDir() && newE.IsDir() {
			if !bytes.Equal(oldE.SubTreeHash, newE.SubTreeHash) {
				return walk(relPath, oldE.SubTreeHash, newE.SubTreeHash)
			}
			return nil
		}
		if !oldE.IsDir() && !newE.IsDir() {
			if !bytes.Equal(oldE.BlobSHA256, newE.BlobSHA256) {
				*touched = append(*touched, relPath)
			}
			return nil
		}
		*touched = append(*touched, relPath)
		return nil
	}
	if inNew {
		if newE.IsDir() {
			return walk(relPath, EmptyTreeHash, newE.SubTreeHash)
		}
		*touched = append(*touched, relPath)
		return nil
	}
	if inOld {
		if oldE.IsDir() {
			return walk(relPath, oldE.SubTreeHash, EmptyTreeHash)
		}
		*touched = append(*touched, relPath)
	}
	return nil
}

// DiffTreeFiles recursively walks and compares oldTree and newTree, returning the slice of relative
// file paths that were added, modified, or deleted.
func DiffTreeFiles(oldTree, newTree []byte, fetchTree FetchTreeFunc) ([]string, error) {
	if bytes.Equal(oldTree, newTree) {
		return nil, nil
	}

	var touched []string
	var walk func(dirPath string, oldHash, newHash []byte) error

	walk = func(dirPath string, oldHash, newHash []byte) error {
		if bytes.Equal(oldHash, newHash) {
			return nil
		}

		oldEntriesMap, err := fetchEntryMap(oldHash, fetchTree)
		if err != nil {
			return err
		}
		newEntriesMap, err := fetchEntryMap(newHash, fetchTree)
		if err != nil {
			return err
		}

		allNames := make(map[string]bool, len(oldEntriesMap)+len(newEntriesMap))
		for n := range oldEntriesMap {
			allNames[n] = true
		}
		for n := range newEntriesMap {
			allNames[n] = true
		}

		for name := range allNames {
			relPath := name
			if dirPath != "" {
				relPath = dirPath + "/" + name
			}
			oldE, inOld := oldEntriesMap[name]
			newE, inNew := newEntriesMap[name]
			if err := compareDiffEntry(relPath, oldE, newE, inOld, inNew, walk, &touched); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk("", oldTree, newTree); err != nil {
		return nil, err
	}
	sort.Strings(touched)
	return touched, nil
}

// SaveTreeFunc persists a computed tree node's entries and returns an error if storage fails.
type SaveTreeFunc func(treeHash []byte, entries []Entry) error

// UpdateFileEntry updates (or inserts) the file at filePath in the Merkle tree rooted at rootTreeHash,
// recursively recomputing and persisting parent directory tree hashes up to the new root.
func UpdateFileEntry(rootTreeHash []byte, filePath string, newBlobSHA256 []byte, newSize uint64, fetchTree FetchTreeFunc, saveTree SaveTreeFunc) ([]byte, error) {
	cleaned := strings.Trim(filepathClean(filePath), "/")
	if cleaned == "" || cleaned == "." {
		return nil, errors.New("filePath cannot be empty")
	}
	segments := strings.Split(cleaned, "/")

	var updateRec func(currentTreeHash []byte, depth int) ([]byte, error)
	updateRec = func(currentTreeHash []byte, depth int) ([]byte, error) {
		var entries []Entry
		if len(currentTreeHash) > 0 && !bytes.Equal(currentTreeHash, EmptyTreeHash) {
			fetched, err := fetchTree(currentTreeHash)
			if err != nil {
				return nil, err
			}
			entries = append(entries, fetched...)
		}

		seg := segments[depth]
		isLeaf := depth == len(segments)-1

		foundIdx := -1
		for i := range entries {
			if entries[i].Name == seg {
				foundIdx = i
				break
			}
		}

		if isLeaf {
			mode := uint32(0100644)
			if foundIdx >= 0 && entries[foundIdx].Mode != 0 {
				mode = entries[foundIdx].Mode
			}
			newEntry := Entry{
				Name:       seg,
				Mode:       mode,
				Size:       newSize,
				BlobSHA256: newBlobSHA256,
			}
			if foundIdx >= 0 {
				entries[foundIdx] = newEntry
			} else {
				entries = append(entries, newEntry)
			}
		} else {
			var subHash []byte
			if foundIdx >= 0 && entries[foundIdx].IsDir() {
				subHash = entries[foundIdx].SubTreeHash
			} else {
				subHash = EmptyTreeHash
			}
			newSubHash, err := updateRec(subHash, depth+1)
			if err != nil {
				return nil, err
			}
			newDirEntry := Entry{
				Name:        seg,
				Mode:        0040755,
				SubTreeHash: newSubHash,
			}
			if foundIdx >= 0 {
				entries[foundIdx] = newDirEntry
			} else {
				entries = append(entries, newDirEntry)
			}
		}

		SortEntries(entries)
		newHash := ComputeTreeHash(entries)
		if saveTree != nil {
			if err := saveTree(newHash, entries); err != nil {
				return nil, err
			}
		}
		return newHash, nil
	}

	return updateRec(rootTreeHash, 0)
}
