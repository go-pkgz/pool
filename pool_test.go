package pool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-pkgz/pool/metrics"
)

func TestPool_Basic(t *testing.T) {
	var processed []string
	var mu sync.Mutex

	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		mu.Lock()
		processed = append(processed, v)
		mu.Unlock()
		return nil
	})

	p := New[string](2, worker)
	require.NoError(t, p.Go(context.Background()))

	inputs := []string{"1", "2", "3", "4", "5"}
	for _, v := range inputs {
		p.Submit(v)
	}

	require.NoError(t, p.Close(context.Background()))

	sort.Strings(processed)
	assert.Equal(t, inputs, processed)
}

func TestPool_ChunkDistribution(t *testing.T) {
	var workerCounts [2]int32

	worker := WorkerFunc[string](func(ctx context.Context, _ string) error {
		id := metrics.WorkerID(ctx)
		atomic.AddInt32(&workerCounts[id], 1)
		return nil
	})

	p := New[string](2, worker).WithChunkFn(func(v string) string { return v })
	require.NoError(t, p.Go(context.Background()))

	// submit same value multiple times, should always go to same worker
	for i := 0; i < 10; i++ {
		p.Submit("test1")
	}
	require.NoError(t, p.Close(context.Background()))

	// verify all items went to the same worker
	assert.True(t, workerCounts[0] == 0 || workerCounts[1] == 0)
	assert.Equal(t, int32(10), workerCounts[0]+workerCounts[1])
}

func TestPool_ErrorHandling_StopOnError(t *testing.T) {
	errTest := errors.New("test error")
	var processedCount atomic.Int32

	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		if v == "error" {
			return errTest
		}
		processedCount.Add(1)
		return nil
	})

	p := New[string](1, worker)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2") // should not be processed

	err := p.Close(context.Background())
	require.ErrorIs(t, err, errTest)
	assert.Equal(t, int32(1), processedCount.Load())
}

func TestPool_ErrorHandling_ContinueOnError(t *testing.T) {
	errTest := errors.New("test error")
	var processedCount atomic.Int32

	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		if v == "error" {
			return errTest
		}
		processedCount.Add(1)
		return nil
	})

	p := New[string](1, worker).WithContinueOnError()
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2")

	err := p.Close(context.Background())
	require.ErrorIs(t, err, errTest)
	assert.Equal(t, int32(2), processedCount.Load())
}

func TestPool_ContextCancellation(t *testing.T) {
	worker := WorkerFunc[string](func(ctx context.Context, _ string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return nil
		}
	})

	p := New[string](1, worker)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NoError(t, p.Go(ctx))
	p.Submit("test")

	err := p.Close(context.Background())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPool_StatefulWorker(t *testing.T) {
	type statefulWorker struct {
		count int
	}

	workerMaker := func() Worker[string] {
		w := &statefulWorker{}
		return WorkerFunc[string](func(_ context.Context, _ string) error {
			w.count++
			time.Sleep(time.Millisecond) // even with sleep it's safe
			return nil
		})
	}

	p := NewStateful[string](2, workerMaker).WithWorkerChanSize(5)
	require.NoError(t, p.Go(context.Background()))

	// submit more items to increase chance of concurrent processing
	for i := 0; i < 100; i++ {
		p.Submit("test")
	}
	assert.NoError(t, p.Close(context.Background()))
}

func TestPool_Wait(t *testing.T) {
	processed := make(map[string]bool)
	var mu sync.Mutex

	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		time.Sleep(10 * time.Millisecond) // simulate work
		mu.Lock()
		processed[v] = true
		mu.Unlock()
		return nil
	})

	p := New[string](2, worker)
	require.NoError(t, p.Go(context.Background()))

	// submit in a separate goroutine since we'll use Wait
	go func() {
		inputs := []string{"1", "2", "3"}
		for _, v := range inputs {
			p.Submit(v)
		}
		err := p.Close(context.Background())
		assert.NoError(t, err)
	}()

	// wait for completion
	require.NoError(t, p.Wait(context.Background()))

	// verify all items were processed
	mu.Lock()
	require.Len(t, processed, 3)
	for _, v := range []string{"1", "2", "3"} {
		require.True(t, processed[v], "item %s was not processed", v)
	}
	mu.Unlock()
}

func TestPool_Wait_WithError(t *testing.T) {
	errTest := errors.New("test error")
	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		if v == "error" {
			return errTest
		}
		return nil
	})

	p := New[string](1, worker)
	require.NoError(t, p.Go(context.Background()))

	go func() {
		p.Submit("ok")
		p.Submit("error")
		err := p.Close(context.Background())
		assert.Error(t, err)
	}()

	err := p.Wait(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errTest)
}

func TestPool_Distribution(t *testing.T) {
	t.Run("shared channel distribution", func(t *testing.T) {
		var counts [2]int32
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			atomic.AddInt32(&counts[metrics.WorkerID(ctx)], 1)
			return nil
		})

		p := New[int](2, worker)
		require.NoError(t, p.Go(context.Background()))

		const n = 10000
		for i := 0; i < n; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		// check both workers got some work
		assert.Positive(t, counts[0], "worker 0 should process some items")
		assert.Positive(t, counts[1], "worker 1 should process some items")

		// check rough distribution, allow more variance as it's scheduler-dependent
		diff := math.Abs(float64(counts[0]-counts[1])) / float64(n)
		require.Less(t, diff, 0.3, "distribution difference %v should be less than 30%%", diff)
		t.Logf("workers distribution: %v, difference: %.2f%%", counts, diff*100)
	})

	t.Run("chunked distribution", func(t *testing.T) {
		var counts [2]int32
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			atomic.AddInt32(&counts[metrics.WorkerID(ctx)], 1)
			return nil
		})

		p := New[int](2, worker).WithChunkFn(func(v int) string {
			return fmt.Sprintf("key-%d", v%10) // 10 different keys
		})
		require.NoError(t, p.Go(context.Background()))

		const n = 10000
		for i := 0; i < n; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		// chunked distribution should still be roughly equal
		diff := math.Abs(float64(counts[0]-counts[1])) / float64(n)
		require.Less(t, diff, 0.1, "chunked distribution difference %v should be less than 10%%", diff)
		t.Logf("workers distribution: %v, difference: %.2f%%", counts, diff*100)
	})
}

func TestPool_Metrics(t *testing.T) {
	t.Run("basic metrics", func(t *testing.T) {
		var processed int32
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			time.Sleep(time.Millisecond) // simulate work
			atomic.AddInt32(&processed, 1)
			return nil
		})

		p := New[int](2, worker)
		require.NoError(t, p.Go(context.Background()))

		for i := 0; i < 10; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		assert.Equal(t, int(atomic.LoadInt32(&processed)), stats.Processed)
		assert.Equal(t, 0, stats.Errors)
		assert.Equal(t, 0, stats.Dropped)
		assert.Greater(t, stats.ProcessingTime, time.Duration(0))
	})

	t.Run("metrics with errors", func(t *testing.T) {
		var errs, processed int32
		worker := WorkerFunc[int](func(ctx context.Context, v int) error {
			if v%2 == 0 {
				atomic.AddInt32(&errs, 1)
				return errors.New("even number")
			}
			atomic.AddInt32(&processed, 1)
			return nil
		})

		p := New[int](2, worker).WithContinueOnError()
		require.NoError(t, p.Go(context.Background()))

		for i := 0; i < 10; i++ {
			p.Submit(i)
		}
		require.Error(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		assert.Equal(t, int(atomic.LoadInt32(&processed)), stats.Processed)
		assert.Equal(t, int(atomic.LoadInt32(&errs)), stats.Errors)
		assert.Equal(t, 0, stats.Dropped)
	})

	t.Run("metrics timing", func(t *testing.T) {
		processed := make(chan struct{}, 2)
		worker := WorkerFunc[int](func(_ context.Context, _ int) error {
			// signal when processing starts
			start := time.Now()
			time.Sleep(10 * time.Millisecond)
			processed <- struct{}{}
			t.Logf("processed item in %v", time.Since(start))
			return nil
		})

		p := New[int](2, worker)
		require.NoError(t, p.Go(context.Background()))

		p.Submit(1)
		p.Submit(2)

		// wait for both items to be processed
		for i := 0; i < 2; i++ {
			select {
			case <-processed:
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for processing")
			}
		}

		require.NoError(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		assert.Equal(t, 2, stats.Processed)

		// verify timing is reasonable but don't be too strict
		assert.Greater(t, stats.ProcessingTime, time.Millisecond,
			"processing time should be measurable")
		assert.Greater(t, stats.TotalTime, time.Millisecond,
			"total time should be measurable")
		assert.Less(t, stats.ProcessingTime, time.Second,
			"processing time should be reasonable")
	})

	t.Run("per worker stats", func(t *testing.T) {
		var processed, errs int32
		worker := WorkerFunc[int](func(ctx context.Context, v int) error {
			time.Sleep(time.Millisecond)
			if v%2 == 0 {
				atomic.AddInt32(&errs, 1)
				return errors.New("even error")
			}
			atomic.AddInt32(&processed, 1)
			return nil
		})

		p := New[int](2, worker).WithContinueOnError()
		require.NoError(t, p.Go(context.Background()))

		// submit enough items to ensure both workers get some
		n := 100
		for i := 0; i < n; i++ {
			p.Submit(i)
		}
		require.Error(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		assert.Equal(t, int(atomic.LoadInt32(&processed)), stats.Processed)
		assert.Equal(t, int(atomic.LoadInt32(&errs)), stats.Errors)
		assert.Greater(t, stats.ProcessingTime, 40*time.Millisecond,
			"processing time should be significant with 100 tasks")
		assert.Less(t, stats.ProcessingTime, time.Second,
			"processing time should be reasonable")
	})
}

func TestPool_MetricsString(t *testing.T) {
	worker := WorkerFunc[int](func(_ context.Context, _ int) error {
		time.Sleep(time.Millisecond)
		return nil
	})

	p := New[int](2, worker)
	require.NoError(t, p.Go(context.Background()))

	p.Submit(1)
	p.Submit(2)
	require.NoError(t, p.Close(context.Background()))

	// check stats string format
	stats := p.Metrics().GetStats()
	str := stats.String()
	assert.Contains(t, str, "processed:2")
	assert.Contains(t, str, "proc:")
	assert.Contains(t, str, "total:")

	// check user metrics string format
	p.Metrics().Add("custom", 5)
	str = p.Metrics().String()
	assert.Contains(t, str, "custom:5")
}

func TestPool_WorkerCompletion(t *testing.T) {
	t.Run("batch processing with errors", func(t *testing.T) {
		var processed []string
		var mu sync.Mutex
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "error" {
				return fmt.Errorf("test error")
			}
			mu.Lock()
			processed = append(processed, v)
			mu.Unlock()
			return nil
		})

		p := New[string](1, worker)
		require.NoError(t, p.Go(context.Background()))

		// submit items including error
		p.Submit("ok1")
		p.Submit("error")
		p.Submit("ok2")

		// should process until error
		err := p.Close(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "test error")
		assert.Equal(t, []string{"ok1"}, processed)
	})

	t.Run("batch processing continues on error", func(t *testing.T) {
		var processed []string
		var mu sync.Mutex
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "error" {
				return fmt.Errorf("test error")
			}
			mu.Lock()
			processed = append(processed, v)
			mu.Unlock()
			return nil
		})

		p := New[string](1, worker).WithContinueOnError()
		require.NoError(t, p.Go(context.Background()))

		// submit items including error
		p.Submit("ok1")
		p.Submit("error")
		p.Submit("ok2")

		// should process all items despite error
		err := p.Close(context.Background())
		require.Error(t, err)
		assert.Equal(t, []string{"ok1", "ok2"}, processed)
	})

	t.Run("completeFn error", func(t *testing.T) {
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return nil
		})

		completeFnError := fmt.Errorf("complete error")
		p := New[string](1, worker).WithCompleteFn(func(context.Context, int, Worker[string]) error {
			return completeFnError
		})
		require.NoError(t, p.Go(context.Background()))

		p.Submit("task")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, completeFnError)
	})

	t.Run("batch error prevents completeFn", func(t *testing.T) {
		var completeFnCalled bool
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return fmt.Errorf("batch error")
		})

		p := New[string](1, worker).WithCompleteFn(func(context.Context, int, Worker[string]) error {
			completeFnCalled = true
			return nil
		})
		require.NoError(t, p.Go(context.Background()))

		p.Submit("task")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "batch error")
		assert.False(t, completeFnCalled, "completeFn should not be called after batch error")
	})

	t.Run("context cancellation", func(t *testing.T) {
		processed := make(chan string, 1)
		worker := WorkerFunc[string](func(ctx context.Context, v string) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case processed <- v:
				return nil
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		p := New[string](1, worker)
		require.NoError(t, p.Go(ctx))

		p.Submit("task")
		cancel()

		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestPool_WaitTimeAccuracy(t *testing.T) {
	t.Run("measures idle time between tasks", func(t *testing.T) {
		worker := WorkerFunc[int](func(ctx context.Context, v int) error {
			time.Sleep(10 * time.Millisecond) // fixed processing time
			return nil
		})

		p := New[int](1, worker)
		require.NoError(t, p.Go(context.Background()))

		// submit first task
		p.Submit(1)
		waitPeriod := 50 * time.Millisecond
		time.Sleep(waitPeriod) // deliberate wait
		p.Submit(2)

		require.NoError(t, p.Close(context.Background()))
		stats := p.Metrics().GetStats()

		// allow for some variance in timing
		minExpectedWait := 35 * time.Millisecond // 70% of wait period
		assert.Greater(t, stats.WaitTime, minExpectedWait,
			"wait time (%v) should be greater than %v", stats.WaitTime, minExpectedWait)
	})
}

func TestPool_InitializationTime(t *testing.T) {
	t.Run("captures initialization in maker function", func(t *testing.T) {
		initDuration := 25 * time.Millisecond

		p := NewStateful[int](1, func() Worker[int] {
			time.Sleep(initDuration) // simulate expensive initialization
			return WorkerFunc[int](func(ctx context.Context, v int) error {
				return nil
			})
		})

		require.NoError(t, p.Go(context.Background()))
		p.Submit(1)
		require.NoError(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		minExpectedInit := 20 * time.Millisecond // 80% of init duration
		assert.Greater(t, stats.InitTime, minExpectedInit,
			"init time (%v) should capture worker maker execution time (expected > %v)",
			stats.InitTime, minExpectedInit)
	})

	t.Run("minimal init time for stateless worker", func(t *testing.T) {
		worker := WorkerFunc[int](func(ctx context.Context, v int) error {
			return nil
		})

		p := New[int](1, worker)
		require.NoError(t, p.Go(context.Background()))
		p.Submit(1)
		require.NoError(t, p.Close(context.Background()))

		stats := p.Metrics().GetStats()
		assert.Less(t, stats.InitTime, 5*time.Millisecond,
			"stateless worker should have minimal init time")
	})
}

func TestPool_TimingUnderLoad(t *testing.T) {
	const (
		workers        = 3
		tasks          = 9
		processingTime = 10 * time.Millisecond
	)

	// create channel to track completion
	done := make(chan struct{}, tasks)

	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		start := time.Now()
		time.Sleep(processingTime)
		t.Logf("task %d processed in %v", v, time.Since(start))
		done <- struct{}{}
		return nil
	})

	p := New[int](workers, worker)
	require.NoError(t, p.Go(context.Background()))

	// submit all tasks
	for i := 0; i < tasks; i++ {
		p.Submit(i)
	}

	// wait for all tasks to complete
	for i := 0; i < tasks; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for tasks to complete")
		}
	}

	require.NoError(t, p.Close(context.Background()))
	stats := p.Metrics().GetStats()

	// verify the basic metrics are reasonable
	assert.Equal(t, tasks, stats.Processed, "all tasks should be processed")
	assert.Greater(t, stats.ProcessingTime, processingTime/2,
		"processing time should be measurable")
	assert.Less(t, stats.ProcessingTime, 5*time.Second,
		"processing time should be reasonable")

	t.Logf("Processed %d tasks with %d workers in %v (processing time %v)",
		stats.Processed, workers, stats.TotalTime, stats.ProcessingTime)
}

func TestMiddleware_Basic(t *testing.T) {
	var processed atomic.Int32

	// create base worker
	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		processed.Add(1)
		return nil
	})

	// middleware to count calls
	var middlewareCalls atomic.Int32
	countMiddleware := func(next Worker[string]) Worker[string] {
		return WorkerFunc[string](func(ctx context.Context, v string) error {
			middlewareCalls.Add(1)
			return next.Do(ctx, v)
		})
	}

	p := New[string](1, worker).Use(countMiddleware)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("test1")
	p.Submit("test2")
	require.NoError(t, p.Close(context.Background()))

	assert.Equal(t, int32(2), processed.Load(), "base worker should process all items")
	assert.Equal(t, int32(2), middlewareCalls.Load(), "middleware should be called for all items")
}

func TestMiddleware_ExecutionOrder(t *testing.T) {
	var order strings.Builder
	var mu sync.Mutex

	addToOrder := func(s string) {
		mu.Lock()
		order.WriteString(s)
		mu.Unlock()
	}

	// base worker
	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		addToOrder("worker->")
		return nil
	})

	// create middlewares that log their execution order
	middleware1 := func(next Worker[string]) Worker[string] {
		return WorkerFunc[string](func(ctx context.Context, v string) error {
			addToOrder("m1_before->")
			err := next.Do(ctx, v)
			addToOrder("m1_after->")
			return err
		})
	}

	middleware2 := func(next Worker[string]) Worker[string] {
		return WorkerFunc[string](func(ctx context.Context, v string) error {
			addToOrder("m2_before->")
			err := next.Do(ctx, v)
			addToOrder("m2_after->")
			return err
		})
	}

	middleware3 := func(next Worker[string]) Worker[string] {
		return WorkerFunc[string](func(ctx context.Context, v string) error {
			addToOrder("m3_before->")
			err := next.Do(ctx, v)
			addToOrder("m3_after->")
			return err
		})
	}

	// apply middlewares: middleware1, middleware2, middleware3
	p := New[string](1, worker).Use(middleware1, middleware2, middleware3)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("test")
	require.NoError(t, p.Close(context.Background()))

	// expect order similar to http middleware: last added = outermost wrapper
	// first added (m1) is closest to worker, last added (m3) is outermost
	expected := "m1_before->m2_before->m3_before->worker->m3_after->m2_after->m1_after->"
	assert.Equal(t, expected, order.String(), "middleware execution order should match HTTP middleware pattern")
}

func TestMiddleware_ErrorHandling(t *testing.T) {
	errTest := errors.New("test error")
	var processed atomic.Int32

	// worker that fails
	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		if v == "error" {
			return errTest
		}
		processed.Add(1)
		return nil
	})

	// middleware that logs errors
	var errCount atomic.Int32
	errorMiddleware := func(next Worker[string]) Worker[string] {
		return WorkerFunc[string](func(ctx context.Context, v string) error {
			err := next.Do(ctx, v)
			if err != nil {
				errCount.Add(1)
			}
			return err
		})
	}

	p := New[string](1, worker).Use(errorMiddleware)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok")
	p.Submit("error")
	err := p.Close(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errTest)

	assert.Equal(t, int32(1), processed.Load(), "should process non-error item")
	assert.Equal(t, int32(1), errCount.Load(), "should count one error")
}

func TestMiddleware_Practical(t *testing.T) {
	t.Run("retry middleware", func(t *testing.T) {
		var attempts atomic.Int32

		// worker that fails first time
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if attempts.Add(1) == 1 {
				return errors.New("temporary error")
			}
			return nil
		})

		// retry middleware
		retryMiddleware := func(maxAttempts int) Middleware[string] {
			return func(next Worker[string]) Worker[string] {
				return WorkerFunc[string](func(ctx context.Context, v string) error {
					var lastErr error
					for i := 0; i < maxAttempts; i++ {
						var err error
						if err = next.Do(ctx, v); err == nil {
							return nil
						}
						lastErr = err

						// wait before retry
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(time.Millisecond):
						}
					}
					return lastErr
				})
			}
		}

		p := New[string](1, worker).Use(retryMiddleware(3))
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, int32(2), attempts.Load(), "should succeed on second attempt")
	})

	t.Run("timing middleware", func(t *testing.T) {
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			time.Sleep(time.Millisecond)
			return nil
		})

		var totalTime int64
		timingMiddleware := func(next Worker[string]) Worker[string] {
			return WorkerFunc[string](func(ctx context.Context, v string) error {
				start := time.Now()
				err := next.Do(ctx, v)
				atomic.AddInt64(&totalTime, time.Since(start).Microseconds())
				return err
			})
		}

		p := New[string](1, worker).Use(timingMiddleware)
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test")
		require.NoError(t, p.Close(context.Background()))

		assert.Greater(t, atomic.LoadInt64(&totalTime), int64(1000),
			"should measure time greater than 1ms")
	})
}
