package server

import (
	"encoding/json"
	"time"
)

// Each collector's cache has both byte and entry limits. Store encoded responses
// instead of retaining large decoded graphs for every tenant/window combination.
const responseCacheBytes = 16 << 20
const responseCacheEntries = 64

type jsonCacheEntry struct {
	at   time.Time
	body json.RawMessage
}

// The caller holds the collector's cache mutex.
func putJSONCache(cache *map[string]jsonCacheEntry, key string, result any, now time.Time) {
	body, err := json.Marshal(result)
	if err != nil || len(body)+len(key) > responseCacheBytes {
		return
	}
	if *cache == nil {
		*cache = make(map[string]jsonCacheEntry)
	}
	delete(*cache, key)
	size := len(body) + len(key)
	for k, entry := range *cache {
		size += len(k) + len(entry.body)
	}
	for size > responseCacheBytes || len(*cache) >= responseCacheEntries {
		oldestKey := ""
		var oldest time.Time
		for k, entry := range *cache {
			if oldest.IsZero() || entry.at.Before(oldest) {
				oldestKey, oldest = k, entry.at
			}
		}
		size -= len(oldestKey) + len((*cache)[oldestKey].body)
		delete(*cache, oldestKey)
	}
	(*cache)[key] = jsonCacheEntry{at: now, body: body}
}
