// Package cache is the shared redis client: a deliberately small surface
// covering the platform's four uses — caching (Get/Set), rate limiting
// (Incr), coordination (SetNX locks / idempotency keys) and the live event
// bus (Publish/Subscribe, see core/ws).
//
// Key-prefix convention: "cache:" for evictable data, "coord:" for keys
// that must never be evicted (locks, rate counters, idempotency). Locally
// one redis serves both; production separates the policies (v3).
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound is returned by Get when the key does not exist (or expired).
var ErrNotFound = errors.New("cache: key not found")

type Config struct {
	Host     string
	Port     string
	Password string // empty for local dev
	DB       int
}

// Cache is a shared, application-unique handle — hold and pass it as a
// pointer, like a DB pool.
type Cache struct {
	client *redis.Client
}

func New(ctx context.Context, cfg Config) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("pinging redis: %w", err)
	}

	return &Cache{client: client}, nil
}

func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("getting %s: %w", key, err)
	}
	return val, nil
}

func (c *Cache) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	if err := c.client.Set(ctx, key, val, ttl).Err(); err != nil {
		return fmt.Errorf("setting %s: %w", key, err)
	}
	return nil
}

// Incr increments the counter and returns the new value. The TTL is armed
// only when the key has none (first hit of the window), so the window never
// slides on subsequent hits — the rate-limit shape.
func (c *Cache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	pipe := c.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("incrementing %s: %w", key, err)
	}
	return incr.Val(), nil
}

// SetNX sets the key only if it does not exist; false means someone else
// holds it — the lock / idempotency-key / cooldown shape.
func (c *Cache) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	ok, err := c.client.SetNX(ctx, key, val, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("setnx %s: %w", key, err)
	}
	return ok, nil
}

// Delete removes a key; deleting a missing key is not an error (releases
// of already-expired locks stay quiet).
func (c *Cache) Delete(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	return nil
}

// Publish hands a payload to every current subscriber of the channel.
// Pub/sub is fire-and-forget: nobody listening is not an error, and nothing
// is retained for a subscriber that connects later — the callers of this
// (live websocket events) all have a durable path of their own.
func (c *Cache) Publish(ctx context.Context, channel, payload string) error {
	if err := c.client.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("publishing to %s: %w", channel, err)
	}
	return nil
}

// Subscribe opens a subscription on the channel. The returned *redis.PubSub
// owns its own connection and reconnects underneath Channel(); the caller
// owns its lifetime and must Close it when it stops listening.
func (c *Cache) Subscribe(ctx context.Context, channel string) *redis.PubSub {
	return c.client.Subscribe(ctx, channel)
}

func (c *Cache) Close() error {
	return c.client.Close()
}
