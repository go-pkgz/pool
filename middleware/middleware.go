// Package middleware provides common middleware implementations for the pool package.
package middleware

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"golang.org/x/time/rate"

	"github.com/go-pkgz/pool"
)

// Retry returns a middleware that retries failed operations up to maxAttempts times
// with exponential backoff between retries.
// baseDelay is used as the initial delay between retries, and each subsequent retry
// increases the delay exponentially (baseDelay * 2^attempt) with some random jitter.
func Retry[T any](maxAttempts int, baseDelay time.Duration) pool.Middleware[T] {
	if maxAttempts <= 0 {
		maxAttempts = 3 // default to 3 attempts
	}
	if baseDelay <= 0 {
		baseDelay = time.Second // default to 1 second
	}

	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) error {
			var lastErr error
			for attempt := range maxAttempts {
				var err error
				if err = next.Do(ctx, v); err == nil {
					return nil
				}
				lastErr = err

				// don't sleep after last attempt
				if attempt < maxAttempts-1 {
					// exponential backoff with jitter
					delay := baseDelay * time.Duration(1<<uint(attempt)) //nolint:gosec // won't overflow, not that many attempts
					// add up to 20% jitter
					jitter := time.Duration(float64(delay) * 0.2 * rand.Float64()) //nolint:gosec // not for security
					delay += jitter

					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
				}
			}
			return fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
		})
	}
}

// Timeout returns a middleware that adds a timeout to each operation.
// If the operation takes longer than the specified timeout, it will be cancelled
// and return context.DeadlineExceeded error.
func Timeout[T any](timeout time.Duration) pool.Middleware[T] {
	if timeout <= 0 {
		timeout = time.Minute // default to 1 minute
	}

	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return next.Do(ctx, v)
		})
	}
}

// Recovery returns a middleware that recovers from panics and converts them to errors.
// If handler is provided, it will be called with the panic value before the error is returned.
func Recovery[T any](handler func(any)) pool.Middleware[T] {
	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) (err error) {
			defer func() {
				if r := recover(); r != nil {
					if handler != nil {
						handler(r)
					}

					// convert panic to error
					switch rt := r.(type) {
					case error:
						err = fmt.Errorf("panic recovered: %w", rt)
					default:
						err = fmt.Errorf("panic recovered: %v", rt)
					}
				}
			}()
			return next.Do(ctx, v)
		})
	}
}

// Validator returns a middleware that validates input values before processing.
// The validator function should return an error if the input is invalid.
func Validator[T any](validator func(T) error) pool.Middleware[T] {
	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) error {
			if err := validator(v); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			return next.Do(ctx, v)
		})
	}
}

// RateLimiter returns a middleware that limits the rate of task processing.
// It uses a token bucket algorithm with the specified rate (tasks/second) and burst size.
// When the rate limit is exceeded, it blocks until a token is available.
// The middleware respects context cancellation - if the context is cancelled while waiting,
// it returns the context error.
// Note: The rate limit is enforced globally across all workers in the pool, not per worker.
//
// Example:
//
//	// Allow 10 tasks per second with a burst of 20
//	pool.Use(middleware.RateLimiter[Task](10, 20))
func RateLimiter[T any](rateLimit float64, burst int) pool.Middleware[T] {
	// validate inputs
	if rateLimit <= 0 {
		rateLimit = 1 // default to 1 task per second
	}
	if burst <= 0 {
		burst = 1 // minimum burst of 1
	}

	// create the rate limiter
	limiter := rate.NewLimiter(rate.Limit(rateLimit), burst)

	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) error {
			// wait for permission to proceed
			if err := limiter.Wait(ctx); err != nil {
				return fmt.Errorf("rate limiter wait failed: %w", err)
			}
			return next.Do(ctx, v)
		})
	}
}
