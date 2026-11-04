package metadata

import (
	"container/list"
	"encoding/hex"
	"path"
	"strings"
	"sync"
	"time"

	"niebla/pkg/tree"
)

const (
	// DefaultMaxCacheEntries is the default capacity for in-memory inodes (e.g. ~100k entries ~= 250MB RSS).
	DefaultMaxCacheEntries = 100000

	// DefaultNegativeTTL is the duration negative lookups (ENOENT) are cached to suppress redundant RPCs.
	DefaultNegativeTTL = 5 * time.Second
)

type cacheItem struct {
	path string
	meta Metadata
}

// MetadataCache stores resolved Merkle tree node attributes in-memory with bounded LRU eviction.
type MetadataCache struct {
	maxEntries   int
	cache        map[string]*list.Element
	lruList      *list.List
	dirChildren  map[string]map[string]bool
	loadedDirs   map[string]bool
	negativeHits map[string]time.Time
	rootTreeHash string
	clID         int64
	mutex        sync.RWMutex
}

// NewMetadataCache initializes an empty MetadataCache with the default entry ceiling.
func NewMetadataCache() *MetadataCache {
	return NewMetadataCacheWithLimit(DefaultMaxCacheEntries)
}

// NewMetadataCacheWithLimit initializes a MetadataCache with an explicit maximum entry capacity.
func NewMetadataCacheWithLimit(maxEntries int) *MetadataCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxCacheEntries
	}
	return &MetadataCache{
		maxEntries:   maxEntries,
		cache:        make(map[string]*list.Element),
		lruList:      list.New(),
		dirChildren:  make(map[string]map[string]bool),
		loadedDirs:   make(map[string]bool),
		negativeHits: make(map[string]time.Time),
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

func normalizeDir(dir string) string {
	cleaned := strings.TrimPrefix(path.Clean(dir), "/")
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}

func pathParent(p string) string {
	idx := strings.LastIndexByte(p, '/')
	if idx == -1 {
		return ""
	}
	return p[:idx]
}

// IsDirectoryLoaded returns true if the entries for the given directory have already been fetched and cached.
func (mc *MetadataCache) IsDirectoryLoaded(dir string) bool {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	return mc.loadedDirs[normalizeDir(dir)]
}

// GetSubtreeHash returns the Merkle tree hash bytes for the specified directory path.
func (mc *MetadataCache) GetSubtreeHash(dir string) ([]byte, bool) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	norm := normalizeDir(dir)
	if norm == "" {
		if mc.rootTreeHash == "" {
			return nil, false
		}
		h, err := hex.DecodeString(mc.rootTreeHash)
		return h, err == nil && len(h) > 0
	}

	elem, exists := mc.cache[norm]
	if !exists {
		return nil, false
	}
	mc.lruList.MoveToFront(elem)
	meta := elem.Value.(*cacheItem).meta
	if meta.SubTreeHash == "" {
		return nil, false
	}
	h, err := hex.DecodeString(meta.SubTreeHash)
	return h, err == nil && len(h) > 0
}

func (mc *MetadataCache) evictOldestLocked() {
	elem := mc.lruList.Back()
	if elem == nil {
		return
	}
	mc.lruList.Remove(elem)
	item, ok := elem.Value.(*cacheItem)
	if !ok || item == nil {
		return
	}
	delete(mc.cache, item.path)
	parent := pathParent(item.path)
	if children, exists := mc.dirChildren[parent]; exists {
		delete(children, item.path)
		if len(children) == 0 {
			delete(mc.dirChildren, parent)
		}
	}
}

func (mc *MetadataCache) putLocked(relPath string, meta Metadata) {
	if elem, exists := mc.cache[relPath]; exists {
		mc.lruList.MoveToFront(elem)
		elem.Value.(*cacheItem).meta = meta
		return
	}

	for len(mc.cache) >= mc.maxEntries && mc.maxEntries > 0 {
		mc.evictOldestLocked()
	}

	item := &cacheItem{path: relPath, meta: meta}
	elem := mc.lruList.PushFront(item)
	mc.cache[relPath] = elem
}

func entryToMetadata(e tree.Entry) Metadata {
	blobHex := ""
	if len(e.BlobSHA256) > 0 {
		blobHex = hex.EncodeToString(e.BlobSHA256)
	}
	subHex := ""
	if len(e.SubTreeHash) > 0 {
		subHex = hex.EncodeToString(e.SubTreeHash)
	}

	return Metadata{
		Name:        e.Name,
		Mode:        e.Mode,
		ByteSize:    int64(e.Size),
		BlobSHA256:  blobHex,
		SubTreeHash: subHex,
	}
}

// PopulateDir records all child entries for dir and marks the directory as loaded.
func (mc *MetadataCache) PopulateDir(dir string, entries []tree.Entry) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	norm := normalizeDir(dir)
	if mc.dirChildren[norm] == nil {
		mc.dirChildren[norm] = make(map[string]bool, len(entries))
	}

	for _, entry := range entries {
		var relPath string
		if norm == "" {
			relPath = entry.Name
		} else {
			relPath = norm + "/" + entry.Name
		}

		meta := entryToMetadata(entry)
		mc.putLocked(relPath, meta)
		mc.dirChildren[norm][relPath] = true
	}
	mc.loadedDirs[norm] = true
}

// Get retrieves cached metadata for a relative path, updating LRU recency.
func (mc *MetadataCache) Get(name string) (Metadata, bool) {
	norm := normalizeDir(name)
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	elem, exists := mc.cache[norm]
	if !exists {
		return Metadata{}, false
	}
	mc.lruList.MoveToFront(elem)
	return elem.Value.(*cacheItem).meta, true
}

// Set stores metadata for a relative path.
func (mc *MetadataCache) Set(name string, meta Metadata) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	norm := normalizeDir(name)
	if meta.Name == "" {
		meta.Name = path.Base(norm)
	}
	mc.putLocked(norm, meta)
	parent := pathParent(norm)
	if mc.dirChildren[parent] == nil {
		mc.dirChildren[parent] = make(map[string]bool)
	}
	mc.dirChildren[parent][norm] = true
	delete(mc.negativeHits, norm)
}

// SetNegative records a negative lookup (ENOENT) with an expiration TTL.
func (mc *MetadataCache) SetNegative(name string, ttl time.Duration) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	if ttl <= 0 {
		ttl = DefaultNegativeTTL
	}
	mc.negativeHits[normalizeDir(name)] = time.Now().Add(ttl)
}

// IsNegative returns true if the relative path is currently recorded as a non-existent entry.
func (mc *MetadataCache) IsNegative(name string) bool {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	norm := normalizeDir(name)
	expiry, exists := mc.negativeHits[norm]
	if !exists {
		return false
	}
	return time.Now().Before(expiry)
}

// ClearNegative removes any negative lookup cache for a path.
func (mc *MetadataCache) ClearNegative(name string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	delete(mc.negativeHits, normalizeDir(name))
}

// Invalidate removes a specific path from the cache.
func (mc *MetadataCache) Invalidate(p string) bool {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	norm := normalizeDir(p)
	elem, exists := mc.cache[norm]
	if exists {
		mc.lruList.Remove(elem)
		delete(mc.cache, norm)
		parent := pathParent(norm)
		if children, cExists := mc.dirChildren[parent]; cExists {
			delete(children, norm)
			if len(children) == 0 {
				delete(mc.dirChildren, parent)
			}
		}
	}
	delete(mc.loadedDirs, norm)
	delete(mc.negativeHits, norm)
	return exists
}

// Purge removes all cached entries and returns the count of purged entries.
func (mc *MetadataCache) Purge() int {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	count := len(mc.cache)
	mc.cache = make(map[string]*list.Element)
	mc.lruList.Init()
	mc.dirChildren = make(map[string]map[string]bool)
	mc.loadedDirs = make(map[string]bool)
	mc.negativeHits = make(map[string]time.Time)
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
	delete(mc.loadedDirs, "")
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

// ListDir returns direct children metadata in the specified relative directory path in O(k) time.
func (mc *MetadataCache) ListDir(dir string) []Metadata {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	cleanDir := normalizeDir(dir)
	children := mc.dirChildren[cleanDir]
	if len(children) == 0 {
		return nil
	}

	results := make([]Metadata, 0, len(children))
	for childPath := range children {
		if elem, ok := mc.cache[childPath]; ok {
			results = append(results, elem.Value.(*cacheItem).meta)
		}
	}
	return results
}

// Reanchor updates the root tree hash and CLID while purging all cached inode metadata.
func (mc *MetadataCache) Reanchor(newRootTreeHash string, clID int64) int {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	count := len(mc.cache)
	mc.cache = make(map[string]*list.Element)
	mc.lruList.Init()
	mc.dirChildren = make(map[string]map[string]bool)
	mc.loadedDirs = make(map[string]bool)
	mc.negativeHits = make(map[string]time.Time)
	mc.rootTreeHash = newRootTreeHash
	if clID > 0 {
		mc.clID = clID
	}
	return count
}
