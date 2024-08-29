package utils

import "hash/fnv"

func ShardIndex(num uint64, key string) int {

	h := fnv.New64a()
	if _, err := h.Write([]byte(key)); err != nil {
		return 0
	}
	return int(h.Sum64() % num)
}
