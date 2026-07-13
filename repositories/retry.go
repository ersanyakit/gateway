package repositories

import (
	"os"
	"strconv"
	"time"
)

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func uintFromEnv(key string, fallback uint) uint {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return fallback
	}
	return uint(value)
}

func exponentialBackoff(attempt uint, base, max time.Duration) time.Duration {
	if attempt == 0 {
		attempt = 1
	}
	delay := base
	for i := uint(1); i < attempt && delay < max; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	if delay > max {
		return max
	}
	return delay
}

func webhookRetryBackoff(attempt uint) time.Duration {
	return exponentialBackoff(
		attempt,
		durationFromEnv("WEBHOOK_RETRY_BACKOFF_BASE", time.Minute),
		durationFromEnv("WEBHOOK_RETRY_BACKOFF_MAX", time.Hour),
	)
}

func sweepRetryBackoff(attempt uint) time.Duration {
	return exponentialBackoff(
		attempt,
		durationFromEnv("SWEEP_RETRY_BACKOFF_BASE", time.Minute),
		durationFromEnv("SWEEP_RETRY_BACKOFF_MAX", time.Hour),
	)
}
