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

func fetchTreeMap(treeHash []byte, label string, fetchTree FetchTreeFunc) (map[string]Entry, error) {
	entryMap := make(map[string]Entry)
	if len(treeHash) > 0 && !bytes.Equal(treeHash, EmptyTreeHash) {
		entries, err := fetchTree(treeHash)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch %s tree %x: %w", label, treeHash, err)
		}
		for _, entry := range entries {
			entryMap[entry.Name] = entry
		}
	}
	return entryMap, nil
}

func mergeBothAddedEntry(
	ctx context.Context,
	name, filePath string,
	hasTheirs, hasMine bool,
	theirs, mine Entry,
	fetchTree FetchTreeFunc,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	writeTree WriteTreeFunc,
	theirsLabel, mineLabel string,
) ([]Entry, []Conflict, error) {
	if hasMine && !hasTheirs {
		return []Entry{mine}, nil, nil
	}
	if !hasMine && hasTheirs {
		return []Entry{theirs}, nil, nil
	}
	if mine.IsDir() && theirs.IsDir() {
		subHash, subConflicts, err := threeWayTreeMergeInternal(
			ctx, filePath, nil, theirs.SubTreeHash, mine.SubTreeHash,
			fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
		)
		if err != nil {
			return nil, nil, err
		}
		var entries []Entry
		if len(subHash) > 0 && !bytes.Equal(subHash, EmptyTreeHash) {
			entries = append(entries, Entry{
				Name:        name,
				Mode:        0040755,
				SubTreeHash: subHash,
			})
		}
		return entries, subConflicts, nil
	}
	if !mine.IsDir() && !theirs.IsDir() {
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

		entries := []Entry{{
			Name:       name,
			Mode:       mine.Mode,
			Size:       uint64(len(mergedBytes)),
			BlobSHA256: blobSHA,
		}}
		var conflicts []Conflict
		if hasConflict {
			conflicts = append(conflicts, Conflict{
				Path:             filePath,
				Reason:           "both_added_conflict",
				ConflictHunks:    hunks,
				ConflictedOutput: mergedBytes,
			})
		}
		return entries, conflicts, nil
	}

	conflicts := []Conflict{{
		Path:   filePath,
		Reason: "type_conflict",
	}}
	return []Entry{mine}, conflicts, nil
}

func mergeChangedBlobs(
	filePath, name string,
	anc, theirs, mine Entry,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	theirsLabel, mineLabel string,
) ([]Entry, []Conflict, error) {
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

	entries := []Entry{{
		Name:       name,
		Mode:       mine.Mode,
		Size:       uint64(len(mergedBytes)),
		BlobSHA256: blobSHA,
	}}
	var conflicts []Conflict
	if hasConflict {
		conflicts = append(conflicts, Conflict{
			Path:             filePath,
			Reason:           "content_conflict",
			ConflictHunks:    hunks,
			ConflictedOutput: mergedBytes,
		})
	}
	return entries, conflicts, nil
}

func mergeBothChangedEntry(
	ctx context.Context,
	name, filePath string,
	hasTheirs, hasMine bool,
	anc, theirs, mine Entry,
	fetchTree FetchTreeFunc,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	writeTree WriteTreeFunc,
	theirsLabel, mineLabel string,
) ([]Entry, []Conflict, error) {
	if !hasTheirs && !hasMine {
		return nil, nil, nil
	}
	if !hasTheirs && hasMine {
		return []Entry{mine}, []Conflict{{Path: filePath, Reason: "delete_modify"}}, nil
	}
	if hasTheirs && !hasMine {
		return []Entry{theirs}, []Conflict{{Path: filePath, Reason: "modify_delete"}}, nil
	}

	if mine.IsDir() && theirs.IsDir() && anc.IsDir() {
		subHash, subConflicts, err := threeWayTreeMergeInternal(
			ctx, filePath, anc.SubTreeHash, theirs.SubTreeHash, mine.SubTreeHash,
			fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
		)
		if err != nil {
			return nil, nil, err
		}
		var entries []Entry
		if len(subHash) > 0 && !bytes.Equal(subHash, EmptyTreeHash) {
			entries = append(entries, Entry{
				Name:        name,
				Mode:        0040755,
				SubTreeHash: subHash,
			})
		}
		return entries, subConflicts, nil
	}

	if !mine.IsDir() && !theirs.IsDir() && !anc.IsDir() {
		return mergeChangedBlobs(filePath, name, anc, theirs, mine, readBlob, writeBlob, theirsLabel, mineLabel)
	}

	return []Entry{mine}, []Conflict{{Path: filePath, Reason: "type_conflict"}}, nil
}

func collectAllNames(ancMap, theirsMap, mineMap map[string]Entry) []string {
	nameSet := make(map[string]struct{}, len(ancMap)+len(theirsMap)+len(mineMap))
	for name := range ancMap {
		nameSet[name] = struct{}{}
	}
	for name := range theirsMap {
		nameSet[name] = struct{}{}
	}
	for name := range mineMap {
		nameSet[name] = struct{}{}
	}

	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveThreeWayEntry(
	ctx context.Context,
	name, filePath string,
	hasAnc, hasTheirs, hasMine bool,
	anc, theirs, mine Entry,
	fetchTree FetchTreeFunc,
	readBlob ReadBlobFunc,
	writeBlob WriteBlobFunc,
	writeTree WriteTreeFunc,
	theirsLabel, mineLabel string,
) ([]Entry, []Conflict, error) {
	if hasMine && hasAnc && equalEntries(mine, anc) {
		if hasTheirs {
			return []Entry{theirs}, nil, nil
		}
		return nil, nil, nil
	}

	if hasTheirs && hasAnc && equalEntries(theirs, anc) {
		if hasMine {
			return []Entry{mine}, nil, nil
		}
		return nil, nil, nil
	}

	if hasMine && hasTheirs && equalEntries(mine, theirs) {
		return []Entry{mine}, nil, nil
	}

	if !hasAnc {
		return mergeBothAddedEntry(
			ctx, name, filePath, hasTheirs, hasMine, theirs, mine,
			fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
		)
	}

	return mergeBothChangedEntry(
		ctx, name, filePath, hasTheirs, hasMine, anc, theirs, mine,
		fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
	)
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
	ancMap, err := fetchTreeMap(ancestorHash, "ancestor", fetchTree)
	if err != nil {
		return nil, nil, err
	}
	theirsMap, err := fetchTreeMap(theirsHash, "theirs", fetchTree)
	if err != nil {
		return nil, nil, err
	}
	mineMap, err := fetchTreeMap(mineHash, "mine", fetchTree)
	if err != nil {
		return nil, nil, err
	}

	names := collectAllNames(ancMap, theirsMap, mineMap)

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

		resEntries, resConflicts, err := resolveThreeWayEntry(
			ctx, name, filePath, hasAnc, hasTheirs, hasMine, anc, theirs, mine,
			fetchTree, readBlob, writeBlob, writeTree, theirsLabel, mineLabel,
		)
		if err != nil {
			return nil, nil, err
		}
		mergedEntries = append(mergedEntries, resEntries...)
		allConflicts = append(allConflicts, resConflicts...)
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
