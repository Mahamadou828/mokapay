package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func GetEnvAsInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}

	return fallback
}

func GetEnvAsTimeDuration(key string, fallback time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := time.ParseDuration(value); err == nil {
			return i
		}
	}
	return fallback
}

// GetEnvAsSlice returns the value of the env var split on commas, with each
// element trimmed of surrounding whitespace. Empty elements are dropped.
// The fallback is returned when the key is unset or holds no usable values.
func GetEnvAsSlice(key string, fallback []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	out := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	if len(out) == 0 {
		return fallback
	}
	return out
}
