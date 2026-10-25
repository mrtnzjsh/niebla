package tree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
)

// ErrMergeConflict is returned when one or more file conflicts or structural conflicts occur.
var ErrMergeConflict = errors.New("merge conflict detected")

// Conflict captures detailed diagnostics for an unresolvable change between parent and child.
type Conflict struct {
	Path             string `json:"path"`
	Reason           string `json:"reason"` // "content_conflict", "modify_delete", "delete_modify", "type_conflict", "both_added_conflict"
	ConflictHunks    int    `json:"conflict_hunks,omitempty"`
	ConflictedOutput []byte `json:"-"`
}

// WriteBlobFunc writes blob bytes to CAS and returns the 32-byte SHA256.
type WriteBlobFunc func(data []byte) ([]byte, error)

// ReadBlobFunc reads blob bytes from CAS given its 32-byte SHA256.
type ReadBlobFunc func(sha []byte) ([]byte, error)

// WriteTreeFunc persists tree entries and returns the computed Merkle tree hash.
type WriteTreeFunc func(entries []Entry) ([]byte, error)

// ThreeWayTreeMerge recursively merges two Merkle tree modifications (theirs and mine)
// relative to a common ancestor tree.
func ThreeWayTreeMerge(
	ctx context.Context,
	ancestorHash []byte,
	theirsHash []byte,
	mineHash []byte,
	fetchTree FetchTreeFunc,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	writeTree WriteTreeFunc,
	theirsLabel, mineLabel string,
) ([]byte, []Conflict, error) {
	// 1. Fast paths
	if bytes.Equal(mineHash, ancestorHash) {
		return append([]byte(nil), theirsHash...), nil, nil
	}
	if bytes.Equal(theirsHash, ancestorHash) {
		return append([]byte(nil), mineHash...), nil, nil
	}
	if bytes.Equal(mineHash, theirsHash) {
		return append([]byte(nil), mineHash...), nil, nil
	}

	mergedHash, conflicts, err := threeWayTreeMergeInternal(
		ctx, "", ancestorHash, theirsHash, mineHash,
		fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
	)

	if len(conflicts) > 0 {
		return mergedHash, conflicts, ErrMergeConflict
	}
	return mergedHash, nil, err
}

func threeWayTreeMergeInternal(
	ctx context.Context,
	dirPath string,
	ancestorHash, theirsHash, mineHash []byte,
	fetchTree FetchTreeFunc,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	writeTree WriteTreeFunc,
	theirsLabel, mineLabel string,
) ([]byte, []Conflict, error) {
	ancMap := make(map[string]Entry)
	theirsMap := make(map[string]Entry)
	mineMap := make(map[string]Entry)

	if len(ancestorHash) > 0 && !bytes.Equal(ancestorHash, EmptyTreeHash) {
		entries, err := fetchTree(ancestorHash)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch ancestor tree %x: %w", ancestorHash, err)
		}
		for _, e := range entries {
			ancMap[e.Name] = e
		}
	}

	if len(theirsHash) > 0 && !bytes.Equal(theirsHash, EmptyTreeHash) {
		entries, err := fetchTree(theirsHash)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch theirs tree %x: %w", theirsHash, err)
		}
		for _, e := range entries {
			theirsMap[e.Name] = e
		}
	}

	if len(mineHash) > 0 && !bytes.Equal(mineHash, EmptyTreeHash) {
		entries, err := fetchTree(mineHash)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch mine tree %x: %w", mineHash, err)
		}
		for _, e := range entries {
			mineMap[e.Name] = e
		}
	}

	// Build unique sorted union of all item names
	nameSet := make(map[string]struct{})
	for k := range ancMap {
		nameSet[k] = struct{}{}
	}
	for k := range theirsMap {
		nameSet[k] = struct{}{}
	}
	for k := range mineMap {
		nameSet[k] = struct{}{}
	}

	names := make([]string, 0, len(nameSet))
	for k := range nameSet {
		names = append(names, k)
	}
	sort.Strings(names)

	var mergedEntries []Entry
	var allConflicts []Conflict

	for _, name := range names {
		filePath := name
		if dirPath != "" {
			filePath = path.Join(dirPath, name)
		}

		anc, hasAnc := ancMap[name]
		theirs, hasTheirs := theirsMap[name]
		mine, hasMine := mineMap[name]

		// 1. Unchanged in mine: accept theirs
		if hasMine && hasAnc && equalEntries(mine, anc) {
			if hasTheirs {
				mergedEntries = append(mergedEntries, theirs)
			}
			// if !hasTheirs, parent deleted it -> omit
			continue
		}

		// 2. Unchanged in theirs: accept mine
		if hasTheirs && hasAnc && equalEntries(theirs, anc) {
			if hasMine {
				mergedEntries = append(mergedEntries, mine)
			}
			// if !hasMine, child deleted it -> omit
			continue
		}

		// 3. Both modified identically
		if hasMine && hasTheirs && equalEntries(mine, theirs) {
			mergedEntries = append(mergedEntries, mine)
			continue
		}

		// 4. Newly added in either or both (not present in ancestor)
		if !hasAnc {
			if hasMine && !hasTheirs {
				mergedEntries = append(mergedEntries, mine)
				continue
			}
			if !hasMine && hasTheirs {
				mergedEntries = append(mergedEntries, theirs)
				continue
			}
			// Both added something with same name
			if mine.IsDir() && theirs.IsDir() {
				subHash, subConflicts, err := threeWayTreeMergeInternal(
					ctx, filePath, nil, theirs.SubTreeHash, mine.SubTreeHash,
					fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
				)
				if err != nil {
					return nil, nil, err
				}
				allConflicts = append(allConflicts, subConflicts...)
				if len(subHash) > 0 && !bytes.Equal(subHash, EmptyTreeHash) {
					mergedEntries = append(mergedEntries, Entry{
						Name:        name,
						Mode:        0040755,
						SubTreeHash: subHash,
					})
				}
				continue
			}
			if !mine.IsDir() && !theirs.IsDir() {
				// Both added files
				theirsBytes, err := readBlob(theirs.BlobSHA256)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to read theirs blob %x for %s: %w", theirs.BlobSHA256, filePath, err)
				}
				mineBytes, err := readBlob(mine.BlobSHA256)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to read mine blob %x for %s: %w", mine.BlobSHA256, filePath, err)
				}

				mergedBytes, hasConflict, hunks := Merge3Way(nil, theirsBytes, mineBytes, theirsLabel, mineLabel)
				blobSHA, err := writeBlob(mergedBytes)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to write merged blob for %s: %w", filePath, err)
				}

				mergedEntries = append(mergedEntries, Entry{
					Name:       name,
					Mode:       mine.Mode,
					Size:       uint64(len(mergedBytes)),
					BlobSHA256: blobSHA,
				})

				if hasConflict {
					allConflicts = append(allConflicts, Conflict{
						Path:             filePath,
						Reason:           "both_added_conflict",
						ConflictHunks:    hunks,
						ConflictedOutput: mergedBytes,
					})
				}
				continue
			}

			// Type mismatch (one added file, one added dir)
			allConflicts = append(allConflicts, Conflict{
				Path:   filePath,
				Reason: "type_conflict",
			})
			mergedEntries = append(mergedEntries, mine)
			continue
		}

		// 5. Present in ancestor, modified/deleted in either or both
		if !hasTheirs && !hasMine {
			// Both deleted
			continue
		}
		if !hasTheirs && hasMine {
			// Parent deleted, child modified
			allConflicts = append(allConflicts, Conflict{
				Path:   filePath,
				Reason: "delete_modify",
			})
			mergedEntries = append(mergedEntries, mine)
			continue
		}
		if hasTheirs && !hasMine {
			// Parent modified, child deleted
			allConflicts = append(allConflicts, Conflict{
				Path:   filePath,
				Reason: "modify_delete",
			})
			mergedEntries = append(mergedEntries, theirs)
			continue
		}

		// Both present and both changed differently
		if mine.IsDir() && theirs.IsDir() && anc.IsDir() {
			subHash, subConflicts, err := threeWayTreeMergeInternal(
				ctx, filePath, anc.SubTreeHash, theirs.SubTreeHash, mine.SubTreeHash,
				fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
			)
			if err != nil {
				return nil, nil, err
			}
			allConflicts = append(allConflicts, subConflicts...)
			if len(subHash) > 0 && !bytes.Equal(subHash, EmptyTreeHash) {
				mergedEntries = append(mergedEntries, Entry{
					Name:        name,
					Mode:        0040755,
					SubTreeHash: subHash,
				})
			}
			continue
		}

		if !mine.IsDir() && !theirs.IsDir() && !anc.IsDir() {
			ancBytes, err := readBlob(anc.BlobSHA256)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read ancestor blob %x for %s: %w", anc.BlobSHA256, filePath, err)
			}
			theirsBytes, err := readBlob(theirs.BlobSHA256)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read theirs blob %x for %s: %w", theirs.BlobSHA256, filePath, err)
			}
			mineBytes, err := readBlob(mine.BlobSHA256)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read mine blob %x for %s: %w", mine.BlobSHA256, filePath, err)
			}

			mergedBytes, hasConflict, hunks := Merge3Way(ancBytes, theirsBytes, mineBytes, theirsLabel, mineLabel)
			blobSHA, err := writeBlob(mergedBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to write merged blob for %s: %w", filePath, err)
			}

			mergedEntries = append(mergedEntries, Entry{
				Name:       name,
				Mode:       mine.Mode,
				Size:       uint64(len(mergedBytes)),
				BlobSHA256: blobSHA,
			})

			if hasConflict {
				allConflicts = append(allConflicts, Conflict{
					Path:             filePath,
					Reason:           "content_conflict",
					ConflictHunks:    hunks,
					ConflictedOutput: mergedBytes,
				})
			}
			continue
		}

		// Type conflict between versions
		allConflicts = append(allConflicts, Conflict{
			Path:   filePath,
			Reason: "type_conflict",
		})
		mergedEntries = append(mergedEntries, mine)
	}

	if len(mergedEntries) == 0 {
		return append([]byte(nil), EmptyTreeHash...), allConflicts, nil
	}

	SortEntries(mergedEntries)
	rootHash, err := writeTree(mergedEntries)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write merged tree at %q: %w", dirPath, err)
	}

	return rootHash, allConflicts, nil
}

func equalEntries(a, b Entry) bool {
	if a.Name != b.Name || a.Mode != b.Mode {
		return false
	}
	if a.IsDir() != b.IsDir() {
		return false
	}
	if a.IsDir() {
		return bytes.Equal(a.SubTreeHash, b.SubTreeHash)
	}
	return bytes.Equal(a.BlobSHA256, b.BlobSHA256)
}
