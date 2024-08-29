package ember

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"goflare.io/ember/utils"
)

func (c *MultiCache) GetFromRemoteCache(ctx context.Context, key string) ([]byte, error) {

	shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
	cb := c.cbMap[uint64(shardIndex)]

	var data []byte

	if _, err := cb.Execute(func() (any, error) {
		var err error
		data, err = c.remoteCache.Get(ctx, key).Bytes()
		return nil, err
	}); err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("redis get failed: %w", err)
	}

	return data, nil
}
