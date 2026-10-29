package metadata

import (
	"sync"
)

type MetadataCache struct {
	cache map[string]Metadata
	mutex sync.RWMutex
}

func NewMetadataCache() *MetadataCache {
	return &MetadataCache{
		cache: make(map[string]Metadata),
	}
}

type Metadata struct {
	Name        string
	Mode        uint32
	ByteSize    int64
	BlobSHA256  string
	SubTreeHash string
}

func (mc *MetadataCache) Get(name string) (Metadata, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	meta, exists := mc.cache[name]
	return meta, exists
}

func (mc *MetadataCache) Set(name string, meta Metadata) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.cache[name] = meta
}
