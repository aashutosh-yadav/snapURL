// Package cache wraps the Redis client used for the cache-aside pattern
// in front of Postgres. Equivalent to redis_client.py.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultTTL = 1 * time.Hour

type Cache struct {
	client *redis.Client
}

func New(addr string) *Cache {
	return &Cache{
		client: redis.NewClient(&redis.Options{
			Addr:        addr,
			DB:          0,
			DialTimeout: 2 * time.Second, // mirrors socket_connect_timeout=2 in the Python client
		}),
	}
}

// Get returns (value, true, nil) on a hit, ("", false, nil) on a clean
// miss, and ("", false, err) if Redis itself failed - callers should
// treat an error the same as a miss (fail open) and log it, matching
// the try/except fallback behavior in the original main.py.
func (c *Cache) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (c *Cache) Set(ctx context.Context, key, value string) error {
	return c.client.Set(ctx, key, value, defaultTTL).Err()
}

func (c *Cache) Close() error {
	return c.client.Close()
}
