package cache_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"mokapay.app/core/cache"
)

// Integration test: needs a running redis. Skipped unless TEST_REDIS_PORT
// is set, so `make test-all` stays green without infra:
//
//	TEST_REDIS_PORT=6379 go test ./core/cache/
func TestCache(t *testing.T) {
	port := os.Getenv("TEST_REDIS_PORT")
	if port == "" {
		t.Skip("TEST_REDIS_PORT not set; integration test needs redis")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := cache.New(ctx, cache.Config{
		Host: getenv("TEST_REDIS_HOST", "localhost"),
		Port: port,
	})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer c.Close()

	// Unique prefix per run keeps the test re-runnable without cleanup.
	p := fmt.Sprintf("test:%d:", time.Now().UnixNano())

	if _, err := c.Get(ctx, p+"missing"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("missing key must be ErrNotFound, got %v", err)
	}

	if err := c.Set(ctx, p+"k", "v", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := c.Get(ctx, p+"k"); err != nil || got != "v" {
		t.Fatalf("Get = %q, %v; want v", got, err)
	}

	// Counter increments and the window TTL arms once.
	for want := int64(1); want <= 3; want++ {
		n, err := c.Incr(ctx, p+"ctr", time.Minute)
		if err != nil || n != want {
			t.Fatalf("Incr = %d, %v; want %d", n, err, want)
		}
	}

	// First SetNX wins, second loses, delete frees it.
	if ok, err := c.SetNX(ctx, p+"lock", "1", time.Minute); err != nil || !ok {
		t.Fatalf("first SetNX = %v, %v; want true", ok, err)
	}
	if ok, err := c.SetNX(ctx, p+"lock", "2", time.Minute); err != nil || ok {
		t.Fatalf("second SetNX = %v, %v; want false", ok, err)
	}
	if err := c.Delete(ctx, p+"lock"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, err := c.SetNX(ctx, p+"lock", "3", time.Minute); err != nil || !ok {
		t.Fatalf("SetNX after delete = %v, %v; want true", ok, err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
