package utils

import (
	"hash/fnv"
)

// ShardIndex 計算分片索引
func ShardIndex(totalShards uint64, key string) uint64 {
	h := fnv.New64a()
	if _, err := h.Write([]byte(key)); err != nil {
		return 0
	}
	return h.Sum64() % totalShards
}
