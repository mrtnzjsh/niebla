package metadata

import (
	"sync"
)

// MetadataCache stores resolved Merkle tree node attributes in-memory to avoid redundant lookups.
type MetadataCache struct {
	cache        map[string]Metadata
	rootTreeHash string
	clID         int64
	mutex        sync.RWMutex
}

// NewMetadataCache initializes an empty MetadataCache.
func NewMetadataCache() *MetadataCache {
	return &MetadataCache{
		cache: make(map[string]Metadata),
	}
}

// Metadata holds cached inode and Merkle node attributes.
type Metadata struct {
	Name        string
	Mode        uint32
	ByteSize    int64
	BlobSHA256  string
	SubTreeHash string
}

// Get retrieves cached metadata for a relative path.
func (mc *MetadataCache) Get(name string) (Metadata, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	meta, exists := mc.cache[name]
	return meta, exists
}

// Set stores metadata for a relative path.
func (mc *MetadataCache) Set(name string, meta Metadata) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.cache[name] = meta
}

// Invalidate removes a specific path from the cache.
func (mc *MetadataCache) Invalidate(path string) bool {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	_, exists := mc.cache[path]
	if exists {
		delete(mc.cache, path)
	}
	return exists
}

// Purge removes all cached entries and returns the count of purged entries.
func (mc *MetadataCache) Purge() int {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	count := len(mc.cache)
	mc.cache = make(map[string]Metadata)
	return count
}

// Count returns the number of currently cached entries.
func (mc *MetadataCache) Count() int {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	return len(mc.cache)
}

// RootTreeHash returns the current active root Merkle tree hash hex string.
func (mc *MetadataCache) RootTreeHash() string {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	return mc.rootTreeHash
}

// SetRootTreeHash updates the active root tree hash.
func (mc *MetadataCache) SetRootTreeHash(h string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.rootTreeHash = h
}

// CLID returns the currently bound changelist ID.
func (mc *MetadataCache) CLID() int64 {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	return mc.clID
}

// SetCLID sets the active changelist ID.
func (mc *MetadataCache) SetCLID(id int64) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.clID = id
}

// ListDir returns all direct children metadata in the specified relative directory path.
func (mc *MetadataCache) ListDir(dir string) []Metadata {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	cleanDir := dir
	if cleanDir == "." || cleanDir == "/" {
		cleanDir = ""
	}

	var results []Metadata
	for p, meta := range mc.cache {
		parent := ""
		idx := -1
		for i := len(p) - 1; i >= 0; i-- {
			if p[i] == '/' {
				idx = i
				break
			}
		}
		if idx != -1 {
			parent = p[:idx]
		}

		if parent == cleanDir {
			results = append(results, meta)
		}
	}
	return results
}

// Reanchor updates the root tree hash and CLID while purging all cached inode metadata.
func (mc *MetadataCache) Reanchor(newRootTreeHash string, clID int64) int {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	count := len(mc.cache)
	mc.cache = make(map[string]Metadata)
	mc.rootTreeHash = newRootTreeHash
	if clID > 0 {
		mc.clID = clID
	}
	return count
}
