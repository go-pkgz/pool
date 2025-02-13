package middleware

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-pkgz/pool"
)

func TestRetry(t *testing.T) {
	t.Run("retries on failure", func(t *testing.T) {
		var attempts atomic.Int32
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			if attempts.Add(1) <= 2 {
				return errors.New("temporary error")
			}
			return nil
		})

		p := pool.New[string](1, worker).Use(Retry[string](3, time.Millisecond))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		require.NoError(t, p.Close(context.Background()))
		assert.Equal(t, int32(3), attempts.Load(), "should retry until success")
	})

	t.Run("fails after max attempts", func(t *testing.T) {
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			return errors.New("persistent error")
		})

		p := pool.New[string](1, worker).Use(Retry[string](2, time.Millisecond))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed after 2 attempts")
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			return errors.New("error")
		})

		p := pool.New[string](1, worker).Use(Retry[string](10, 20*time.Millisecond))
		require.NoError(t, p.Go(ctx))

		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestTimeout(t *testing.T) {
	t.Run("allows fast operations", func(t *testing.T) {
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			time.Sleep(time.Millisecond)
			return nil
		})

		p := pool.New[string](1, worker).Use(Timeout[string](100 * time.Millisecond))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		require.NoError(t, p.Close(context.Background()))
	})

	t.Run("cancels slow operations", func(t *testing.T) {
		worker := pool.WorkerFunc[string](func(ctx context.Context, v string) error {
			select {
			case <-time.After(100 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

		p := pool.New[string](1, worker).Use(Timeout[string](10 * time.Millisecond))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestRecover(t *testing.T) {
	t.Run("recovers from panic", func(t *testing.T) {
		var recovered interface{}
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "panic" {
				panic("test panic")
			}
			return nil
		})

		p := pool.New[string](1, worker).Use(Recovery[string](func(p interface{}) {
			recovered = p
		}))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("panic")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic recovered")
		assert.Equal(t, "test panic", recovered)
	})

	t.Run("allows normal operations", func(t *testing.T) {
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			return nil
		})

		var recovered interface{}
		p := pool.New[string](1, worker).Use(Recovery[string](func(p interface{}) {
			recovered = p
		}))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("ok")
		require.NoError(t, p.Close(context.Background()))
		assert.Nil(t, recovered, "should not call recover handler")
	})

	t.Run("recovers and converts errors", func(t *testing.T) {
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			panic(fmt.Errorf("custom error"))
		})

		p := pool.New[string](1, worker).Use(Recovery[string](nil)) // nil handler is valid
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic recovered: custom error")
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid input passes through", func(t *testing.T) {
		validator := func(s string) error {
			if len(s) >= 3 {
				return nil
			}
			return errors.New("string too short")
		}

		var processed []string
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			processed = append(processed, v)
			return nil
		})

		p := pool.New[string](1, worker).Use(Validator(validator))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, []string{"test"}, processed)
	})

	t.Run("invalid input blocked", func(t *testing.T) {
		validator := func(s string) error {
			if len(s) >= 3 {
				return nil
			}
			return errors.New("string too short")
		}

		var processed []string
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			processed = append(processed, v)
			return nil
		})

		p := pool.New[string](1, worker).Use(Validator(validator))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("ok")
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validation failed")
		assert.Empty(t, processed)
	})
}
