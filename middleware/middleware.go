// Package middleware provides common middleware implementations for the pool package.
package middleware

import (
	"context"
	"fmt"
	"math/rand"
	"time"

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
func Recovery[T any](handler func(interface{})) pool.Middleware[T] {
	return func(next pool.Worker[T]) pool.Worker[T] {
		return pool.WorkerFunc[T](func(ctx context.Context, v T) (err error) {
			defer func() {
				if r := recover(); r != nil {
					if handler != nil {
						handler(r)
					}

					// Convert panic to error
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
