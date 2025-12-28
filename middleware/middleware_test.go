package middleware

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
		var recovered any
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "panic" {
				panic("test panic")
			}
			return nil
		})

		p := pool.New[string](1, worker).Use(Recovery[string](func(p any) {
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

		var recovered any
		p := pool.New[string](1, worker).Use(Recovery[string](func(p any) {
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

func TestRateLimiter(t *testing.T) {
	t.Run("allows tasks within rate limit", func(t *testing.T) {
		var processed atomic.Int32
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			processed.Add(1)
			return nil
		})

		// 10 tasks per second with burst of 5
		p := pool.New[string](2, worker).Use(RateLimiter[string](10, 5))
		require.NoError(t, p.Go(context.Background()))

		// submit 5 tasks - should all process immediately due to burst
		for range 5 {
			p.Submit("task")
		}

		// wait a bit for processing
		time.Sleep(50 * time.Millisecond)
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, int32(5), processed.Load(), "all tasks within burst should process")
	})

	t.Run("blocks tasks exceeding rate limit", func(t *testing.T) {
		var timestamps []time.Time
		var mu sync.Mutex
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			mu.Lock()
			timestamps = append(timestamps, time.Now())
			mu.Unlock()
			return nil
		})

		// 2 tasks per second with burst of 1
		p := pool.New[string](1, worker).Use(RateLimiter[string](2, 1))
		require.NoError(t, p.Go(context.Background()))

		// submit 3 tasks
		for i := range 3 {
			p.Submit(fmt.Sprintf("task-%d", i))
		}

		require.NoError(t, p.Close(context.Background()))

		// verify timing
		require.Len(t, timestamps, 3)
		// first task should process immediately
		// second task should wait ~500ms (rate of 2/sec)
		// third task should wait another ~500ms
		gap1 := timestamps[1].Sub(timestamps[0])
		gap2 := timestamps[2].Sub(timestamps[1])

		assert.Greater(t, gap1, 350*time.Millisecond, "second task should wait for rate limit")
		assert.Less(t, gap1, 650*time.Millisecond, "second task shouldn't wait too long")
		assert.Greater(t, gap2, 350*time.Millisecond, "third task should wait for rate limit")
		assert.Less(t, gap2, 650*time.Millisecond, "third task shouldn't wait too long")
	})

	t.Run("respects context cancellation while waiting", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		var processed atomic.Int32
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			processed.Add(1)
			time.Sleep(10 * time.Millisecond) // simulate work
			return nil
		})

		// very low rate to force waiting
		p := pool.New[string](1, worker).Use(RateLimiter[string](1, 1))
		require.NoError(t, p.Go(ctx))

		// submit multiple tasks
		for range 5 {
			p.Submit("task")
		}

		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "would exceed context deadline")
		// should process 1-2 tasks before context cancellation
		assert.Less(t, processed.Load(), int32(3), "should not process all tasks")
	})

	t.Run("handles default values", func(t *testing.T) {
		var processed atomic.Int32
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			processed.Add(1)
			return nil
		})

		// test with invalid rate and burst
		p := pool.New[string](1, worker).Use(RateLimiter[string](-1, 0))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("task")
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, int32(1), processed.Load(), "should process with default values")
	})

	t.Run("multiple workers share rate limit", func(t *testing.T) {
		var timestamps []time.Time
		var mu sync.Mutex
		worker := pool.WorkerFunc[string](func(_ context.Context, v string) error {
			mu.Lock()
			timestamps = append(timestamps, time.Now())
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // simulate work
			return nil
		})

		// 2 tasks per second with burst of 2, but 4 workers
		p := pool.New[string](4, worker).Use(RateLimiter[string](2, 2))
		require.NoError(t, p.Go(context.Background()))

		// submit 4 tasks
		for i := range 4 {
			p.Submit(fmt.Sprintf("task-%d", i))
		}

		require.NoError(t, p.Close(context.Background()))

		// verify timing - even with 4 workers, rate limit is shared
		require.Len(t, timestamps, 4)
		// first 2 tasks should process immediately (burst)
		gap1 := timestamps[1].Sub(timestamps[0])
		assert.Less(t, gap1, 50*time.Millisecond, "first two tasks should process immediately")

		// next 2 tasks should wait for rate limit
		gap2 := timestamps[2].Sub(timestamps[1])
		gap3 := timestamps[3].Sub(timestamps[2])
		assert.Greater(t, gap2, 350*time.Millisecond, "third task should wait for rate limit")
		assert.Greater(t, gap3, 350*time.Millisecond, "fourth task should wait for rate limit")
	})
}
