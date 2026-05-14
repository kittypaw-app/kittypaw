package llm

import (
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const providerRetryAfterMaxDelay = 30 * time.Second

func providerRetryDelay(base time.Duration, attempt int, retryAfter string, now time.Time) time.Duration {
	if delay, ok := parseRetryAfterDelay(retryAfter, now); ok {
		if delay > providerRetryAfterMaxDelay {
			return providerRetryAfterMaxDelay
		}
		return delay
	}
	return time.Duration(float64(base) * math.Pow(2, float64(attempt-1)) * (0.5 + rand.Float64()))
}

func parseRetryAfterDelay(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if delay := retryAt.Sub(now); delay > 0 {
		return delay, true
	}
	return 0, true
}
