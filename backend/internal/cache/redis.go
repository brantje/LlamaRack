package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisOperationTimeout = 250 * time.Millisecond

type Redis struct {
	client *redis.Client
}

func NewRedis(rawURL string) (*Redis, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid Redis URL")
	}
	// Explicit bounds keep an optional cache from becoming request-critical,
	// independent of client-library default changes.
	options.DialTimeout = redisOperationTimeout
	options.ReadTimeout = redisOperationTimeout
	options.WriteTimeout = redisOperationTimeout
	options.PoolTimeout = redisOperationTimeout
	options.MaxRetries = -1
	return &Redis{client: redis.NewClient(options)}, nil
}

func (r *Redis) Get(ctx context.Context, key string, dst any) (bool, error) {
	if r == nil || r.client == nil || dst == nil {
		return false, errors.New("Redis cache is not configured")
	}
	opCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
	defer cancel()
	payload, err := r.client.Get(opCtx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		_ = r.Delete(ctx, key)
		return false, err
	}
	return true, nil
}

func (r *Redis) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return errors.New("Redis cache is not configured")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
	defer cancel()
	return r.client.Set(opCtx, key, payload, ttl).Err()
}

func (r *Redis) Delete(ctx context.Context, key string) error {
	if r == nil || r.client == nil {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
	defer cancel()
	return r.client.Del(opCtx, key).Err()
}

func (r *Redis) Ping(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("Redis cache is not configured")
	}
	opCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
	defer cancel()
	return r.client.Ping(opCtx).Err()
}

func (r *Redis) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}
