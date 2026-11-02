package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResponseCacheByteLimitAndImmutableSnapshots(t *testing.T) {
	var cache map[string]jsonCacheEntry
	now := time.Now()
	value := map[string]string{"namespace": "team-a"}
	putJSONCache(&cache, "first", value, now)
	value["namespace"] = "team-b"
	var decoded map[string]string
	if err := json.Unmarshal(cache["first"].body, &decoded); err != nil || decoded["namespace"] != "team-a" {
		t.Fatal("cache shared mutable result")
	}
	large := strings.Repeat("a", 9<<20)
	putJSONCache(&cache, "large-one", large, now.Add(time.Second))
	putJSONCache(&cache, "large-two", large, now.Add(2*time.Second))
	total := 0
	for k, v := range cache {
		total += len(k) + len(v.body)
	}
	if total > responseCacheBytes || len(cache) != 1 || cache["large-two"].body == nil {
		t.Fatal("byte bound/oldest eviction failed", total, len(cache))
	}
	putJSONCache(&cache, "oversized", strings.Repeat("b", responseCacheBytes), now)
	if _, ok := cache["oversized"]; ok {
		t.Fatal("oversized entry retained")
	}
}
