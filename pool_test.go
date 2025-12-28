package pool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
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
	for range 10 {
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

	var totalProcessed atomic.Int32

	workerMaker := func() Worker[string] {
		w := &statefulWorker{}
		return WorkerFunc[string](func(_ context.Context, _ string) error {
			w.count++
			totalProcessed.Add(1)
			time.Sleep(time.Millisecond) // even with sleep it's safe
			return nil
		})
	}

	p := NewStateful[string](2, workerMaker).WithWorkerChanSize(5)
	require.NoError(t, p.Go(context.Background()))

	// submit more items to increase chance of concurrent processing
	for range 100 {
		p.Submit("test")
	}
	assert.NoError(t, p.Close(context.Background()))
	assert.Equal(t, int32(100), totalProcessed.Load(), "all items should be processed by stateful workers")
}

func TestPool_WithWorkerChanSize(t *testing.T) {
	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		return nil
	})

	t.Run("New with custom channel size", func(t *testing.T) {
		p := New[int](2, worker).WithWorkerChanSize(10)

		// channels should have the configured size
		for i, ch := range p.workersCh {
			assert.Equal(t, 10, cap(ch), "worker channel %d should have capacity 10", i)
		}
		for i, ch := range p.workerBatchCh {
			assert.Equal(t, 10, cap(ch), "batch channel %d should have capacity 10", i)
		}
	})

	t.Run("NewStateful with custom channel size", func(t *testing.T) {
		p := NewStateful[int](2, func() Worker[int] { return worker }).WithWorkerChanSize(15)

		// channels should have the configured size
		for i, ch := range p.workersCh {
			assert.Equal(t, 15, cap(ch), "worker channel %d should have capacity 15", i)
		}
		for i, ch := range p.workerBatchCh {
			assert.Equal(t, 15, cap(ch), "batch channel %d should have capacity 15", i)
		}
	})

	t.Run("default channel size", func(t *testing.T) {
		p := New[int](2, worker)
		// without WithWorkerChanSize, should use default size of 1
		for i, ch := range p.workersCh {
			assert.Equal(t, 1, cap(ch), "worker channel %d should have default capacity 1", i)
		}
	})

	t.Run("shared channels with custom size", func(t *testing.T) {
		// when WithChunkFn is not used, workers use shared channels
		p := New[int](2, worker).WithWorkerChanSize(20)

		assert.Equal(t, 20, cap(p.sharedCh), "shared channel should have capacity 20")
		assert.Equal(t, 20, cap(p.sharedBatchCh), "shared batch channel should have capacity 20")
	})

	t.Run("shared channels default size", func(t *testing.T) {
		// shared channels default to poolSize for better throughput with multiple consumers
		p := New[int](2, worker)

		assert.Equal(t, 2, cap(p.sharedCh), "shared channel should have default capacity equal to poolSize")
		assert.Equal(t, 2, cap(p.sharedBatchCh), "shared batch channel should have default capacity equal to poolSize")
	})
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
		for i := range n {
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
		for i := range n {
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

		for i := range 10 {
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

		for i := range 10 {
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

		p := New[int](2, worker).WithBatchSize(0) // disable batching
		require.NoError(t, p.Go(context.Background()))

		p.Submit(1)
		p.Submit(2)

		// wait for both items to be processed
		for range 2 {
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
		for i := range n {
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

	t.Run("workerCompleteFn error", func(t *testing.T) {
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return nil
		})

		completeFnError := fmt.Errorf("complete error")
		p := New[string](1, worker).WithWorkerCompleteFn(func(context.Context, int, Worker[string]) error {
			return completeFnError
		})
		require.NoError(t, p.Go(context.Background()))

		p.Submit("task")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, completeFnError)
	})

	t.Run("batch error prevents workerCompleteFn", func(t *testing.T) {
		var completeFnCalled bool
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return fmt.Errorf("batch error")
		})

		p := New[string](1, worker).WithWorkerCompleteFn(func(context.Context, int, Worker[string]) error {
			completeFnCalled = true
			return nil
		})
		require.NoError(t, p.Go(context.Background()))

		p.Submit("task")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "batch error")
		assert.False(t, completeFnCalled, "workerCompleteFn should not be called after batch error")
	})

	t.Run("context cancellation", func(t *testing.T) {
		processed := make(chan string, 1)
		worker := WorkerFunc[string](func(ctx context.Context, v string) error {
			// make sure we wait for context cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			case processed <- v:
				time.Sleep(50 * time.Millisecond) // ensure we're still processing when cancelled
				return ctx.Err()
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		p := New[string](1, worker).WithBatchSize(0) // disable batching for this test
		require.NoError(t, p.Go(ctx))

		p.Submit("task")

		// wait for task to start processing
		select {
		case <-processed:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for processing to start")
		}

		// ensure the task is being processed
		time.Sleep(10 * time.Millisecond)
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

	p := New[int](workers, worker).WithBatchSize(0)
	require.NoError(t, p.Go(context.Background()))

	// submit all tasks
	for i := range tasks {
		p.Submit(i)
	}

	// wait for all tasks to complete
	for range tasks {
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

	t.Run("stateless worker  middleware", func(t *testing.T) {
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
	})

	t.Run("stateful worker middleware", func(t *testing.T) {
		type statefulWorker struct {
			count int
		}

		var order []string
		var mu sync.Mutex

		// create stateful worker
		maker := func() Worker[string] {
			w := &statefulWorker{}
			return WorkerFunc[string](func(_ context.Context, v string) error {
				w.count++
				mu.Lock()
				order = append(order, fmt.Sprintf("worker_%d", w.count))
				mu.Unlock()
				return nil
			})
		}

		// create simple logging middleware
		logMiddleware := func(next Worker[string]) Worker[string] {
			return WorkerFunc[string](func(ctx context.Context, v string) error {
				mu.Lock()
				order = append(order, "middleware_before")
				mu.Unlock()
				err := next.Do(ctx, v)
				mu.Lock()
				order = append(order, "middleware_after")
				mu.Unlock()
				return err
			})
		}

		p := NewStateful[string](1, maker).Use(logMiddleware)
		require.NoError(t, p.Go(context.Background()))

		p.Submit("test1")
		p.Submit("test2")
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, []string{
			"middleware_before",
			"worker_1",
			"middleware_after",
			"middleware_before",
			"worker_2",
			"middleware_after",
		}, order, "middleware should wrap each worker call")
	})
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
					for range maxAttempts {
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

func TestPool_Batch(t *testing.T) {
	t.Run("basic batching", func(t *testing.T) {
		var batches [][]string
		var mu sync.Mutex

		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			mu.Lock()
			defer mu.Unlock()

			// start new batch if no batches or current batch is full
			if len(batches) == 0 || len(batches[len(batches)-1]) >= 3 {
				batches = append(batches, []string{v})
				return nil
			}

			// add to current batch
			batches[len(batches)-1] = append(batches[len(batches)-1], v)
			return nil
		})

		p := New[string](2, worker).WithBatchSize(3)
		require.NoError(t, p.Go(context.Background()))

		// submit 8 items - should make 2 full batches and 1 partial
		for i := range 8 {
			p.Submit(fmt.Sprintf("v%d", i))
		}
		require.NoError(t, p.Close(context.Background()))

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, batches, 3, "should have 3 batches")
		assert.Len(t, batches[0], 3, "first batch should be full")
		assert.Len(t, batches[1], 3, "second batch should be full")
		assert.Len(t, batches[2], 2, "last batch should have remaining items")
	})

	t.Run("batching with chunk function", func(t *testing.T) {
		var processed sync.Map

		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			key := v[:1] // first letter is the chunk key
			val, _ := processed.LoadOrStore(key, []string{})
			items := val.([]string)
			items = append(items, v)
			processed.Store(key, items)
			return nil
		})

		p := New[string](2, worker).
			WithBatchSize(2).
			WithChunkFn(func(v string) string { return v[:1] }) // chunk by first letter

		require.NoError(t, p.Go(context.Background()))

		// submit items that should go to different workers
		items := []string{"a1", "a2", "a3", "b1", "b2", "b3"}
		for _, item := range items {
			p.Submit(item)
		}
		require.NoError(t, p.Close(context.Background()))

		// verify items are grouped by first letter
		aItems, _ := processed.Load("a")
		bItems, _ := processed.Load("b")
		assert.Len(t, aItems.([]string), 3, "should have 3 'a' items")
		assert.Len(t, bItems.([]string), 3, "should have 3 'b' items")
	})

	t.Run("error handling in batch", func(t *testing.T) {
		var processed []string
		var mu sync.Mutex

		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if strings.HasPrefix(v, "err") {
				return fmt.Errorf("error processing %s", v)
			}
			mu.Lock()
			processed = append(processed, v)
			mu.Unlock()
			return nil
		})

		// test without continue on error
		p1 := New[string](1, worker).WithBatchSize(2)
		require.NoError(t, p1.Go(context.Background()))

		p1.Submit("ok1")
		p1.Submit("err1")
		p1.Submit("ok2") // should not be processed due to error

		err := p1.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error processing err1")

		mu.Lock()
		assert.Equal(t, []string{"ok1"}, processed)
		mu.Unlock()

		// test with continue on error
		processed = nil // reset
		p2 := New[string](1, worker).WithBatchSize(2).WithContinueOnError()
		require.NoError(t, p2.Go(context.Background()))

		p2.Submit("ok3")
		p2.Submit("err2")
		p2.Submit("ok4")

		err = p2.Close(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error processing err2")

		mu.Lock()
		assert.Equal(t, []string{"ok3", "ok4"}, processed)
		mu.Unlock()
	})

	t.Run("batch metrics", func(t *testing.T) {
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			time.Sleep(time.Millisecond) // ensure some processing time
			if strings.HasPrefix(v, "err") {
				return fmt.Errorf("error processing %s", v)
			}
			return nil
		})

		p := New[string](1, worker).WithBatchSize(3).WithContinueOnError()
		require.NoError(t, p.Go(context.Background()))

		// submit exactly 9 items: 6 good (ok) and 3 errors (err)
		items := []string{
			"ok1", "ok2", "err0", // batch 1
			"ok3", "ok4", "err1", // batch 2
			"ok5", "ok6", "err2", // batch 3
		}
		for _, item := range items {
			p.Submit(item)
		}

		err := p.Close(context.Background())
		require.Error(t, err) // should have errors but continue

		stats := p.Metrics().GetStats()
		assert.Equal(t, 6, stats.Processed, "should process all ok items")
		assert.Equal(t, 3, stats.Errors, "should count error items")
		assert.Greater(t, stats.ProcessingTime, time.Duration(0),
			"should accumulate processing time")
	})

	t.Run("batch context cancellation", func(t *testing.T) {
		// when context is cancelled, the pool guarantees:
		// 1. full batches are processed before shutdown
		// 2. no new items will be accepted
		// 3. partial batches may be discarded for clean shutdown
		// 4. workers receive proper context cancellation error
		var processed []string
		var mu sync.Mutex
		var errFound atomic.Bool

		worker := WorkerFunc[string](func(ctx context.Context, v string) error {
			if ctx.Err() != nil {
				errFound.Store(true)
				return ctx.Err()
			}
			mu.Lock()
			processed = append(processed, v)
			mu.Unlock()
			return nil
		})

		p := New[string](1, worker).WithBatchSize(3).WithContinueOnError()
		require.NoError(t, p.Go(context.Background()))

		// fill batches with items to verify processing of full batches
		for i := range 6 {
			p.Submit(fmt.Sprintf("item%d", i))
			time.Sleep(10 * time.Millisecond) // allow time for processing
		}

		require.NoError(t, p.Close(context.Background()))

		// verify that full batches were processed
		mu.Lock()
		sort.Strings(processed) // sort for deterministic comparison
		t.Logf("processed items: %v", processed)
		require.NotEmpty(t, processed, "at least some items should be processed")
		require.Len(t, processed, 6, "both full batches should be processed")
		mu.Unlock()
	})
	t.Run("batch timing and ordering", func(t *testing.T) {
		var batches [][]string
		var mu sync.Mutex
		processTime := 10 * time.Millisecond

		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			time.Sleep(processTime) // simulate work
			mu.Lock()
			batches = append(batches, []string{v})
			mu.Unlock()
			return nil
		})

		start := time.Now()
		p := New[string](2, worker).WithBatchSize(3)
		require.NoError(t, p.Go(context.Background()))

		// submit items with timing gap
		p.Submit("fast1")
		p.Submit("fast2")
		time.Sleep(50 * time.Millisecond)
		p.Submit("slow1") // should create new batch due to timing gap
		p.Submit("slow2")

		require.NoError(t, p.Close(context.Background()))
		elapsed := time.Since(start)

		mu.Lock()
		defer mu.Unlock()

		// verify batches are processed as expected
		assert.GreaterOrEqual(t, len(batches), 2, "should have at least 2 batches")
		totalItems := 0
		for _, batch := range batches {
			totalItems += len(batch)
		}
		assert.Equal(t, 4, totalItems, "all items should be processed")
		assert.Greater(t, elapsed, 50*time.Millisecond, "should respect timing gaps between batches")
	})

	t.Run("concurrent batch processing", func(t *testing.T) {
		const (
			numWorkers = 3
			batchSize  = 3
			totalItems = 12
		)

		var (
			batchStartTimes = make(map[int]time.Time)
			batchEndTimes   = make(map[int]time.Time)
			mu              sync.Mutex
		)

		worker := WorkerFunc[int](func(_ context.Context, v int) error {
			batchID := v / batchSize

			mu.Lock()
			if _, exists := batchStartTimes[batchID]; !exists {
				batchStartTimes[batchID] = time.Now()
			}
			mu.Unlock()

			// simulate some work
			time.Sleep(20 * time.Millisecond)

			mu.Lock()
			batchEndTimes[batchID] = time.Now()
			mu.Unlock()
			return nil
		})

		p := New[int](numWorkers, worker).WithBatchSize(batchSize)
		require.NoError(t, p.Go(context.Background()))

		// submit items
		for i := range totalItems {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		mu.Lock()
		defer mu.Unlock()

		// verify all batches were processed
		assert.Len(t, batchStartTimes, totalItems/batchSize, "should process all batches")

		// verify concurrent processing by checking for overlapping time ranges
		var overlapped bool
		for i := range totalItems / batchSize {
			for j := i + 1; j < totalItems/batchSize; j++ {
				// check if batch i and j overlapped in time
				if !batchEndTimes[i].Before(batchStartTimes[j]) && !batchEndTimes[j].Before(batchStartTimes[i]) {
					overlapped = true
					break
				}
			}
			if overlapped {
				break
			}
		}
		assert.True(t, overlapped, "should have overlapping batch processing times indicating concurrency")
	})
}

func TestPool_DirectModeChunking(t *testing.T) {
	var processed sync.Map

	worker := WorkerFunc[string](func(ctx context.Context, v string) error {
		wid := metrics.WorkerID(ctx)
		key := fmt.Sprintf("worker-%d", wid)
		items, _ := processed.LoadOrStore(key, []string{})
		processed.Store(key, append(items.([]string), v))
		return nil
	})

	// create pool with chunk function but no batching and  chunk by first letter
	p := New[string](2, worker).WithBatchSize(0).WithChunkFn(func(v string) string { return v[:1] })

	require.NoError(t, p.Go(context.Background()))

	// submit items that should go to different workers
	items := []string{"a1", "a2", "b1", "b2"}
	for _, item := range items {
		p.Submit(item)
	}

	require.NoError(t, p.Close(context.Background()))

	// verify items with same first letter went to the same worker
	var worker0Items, worker1Items []string
	processed.Range(func(key, value any) bool {
		items := value.([]string)
		if key.(string) == "worker-0" {
			worker0Items = items
		} else {
			worker1Items = items
		}
		return true
	})

	// all "a" items should be in one worker, all "b" in another
	require.Len(t, worker0Items, 2)
	require.Len(t, worker1Items, 2)

	firstWorkerPrefix := worker0Items[0][:1]
	for _, item := range worker0Items {
		assert.Equal(t, firstWorkerPrefix, item[:1], "items in worker 0 should have same prefix")
	}

	secondWorkerPrefix := worker1Items[0][:1]
	for _, item := range worker1Items {
		assert.Equal(t, secondWorkerPrefix, item[:1], "items in worker 1 should have same prefix")
	}

	assert.NotEqual(t, firstWorkerPrefix, secondWorkerPrefix, "workers should handle different prefixes")
}

func TestPool_PoolCompletion(t *testing.T) {
	t.Run("called once after all workers done", func(t *testing.T) {
		var workersCompleted, poolCompleteCalls atomic.Int32

		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			time.Sleep(time.Millisecond) // ensure work takes some time
			workersCompleted.Add(1)
			return nil
		})

		p := New[string](3, worker).
			WithWorkerCompleteFn(func(context.Context, int, Worker[string]) error {
				return nil // worker completion should still work
			}).
			WithPoolCompleteFn(func(context.Context) error {
				poolCompleteCalls.Add(1)
				return nil
			})

		require.NoError(t, p.Go(context.Background()))

		// submit enough work for all workers
		for i := range 3 {
			p.Submit(fmt.Sprintf("test%d", i))
		}
		require.NoError(t, p.Close(context.Background()))

		assert.Equal(t, int32(3), workersCompleted.Load(),
			"all workers should process items")
		assert.Equal(t, int32(1), poolCompleteCalls.Load(),
			"pool completion callback should be called exactly once")
	})

	t.Run("pool completion error handling", func(t *testing.T) {
		errTest := errors.New("test error")
		worker := WorkerFunc[string](func(_ context.Context, _ string) error {
			return nil
		})

		p := New[string](1, worker).
			WithPoolCompleteFn(func(context.Context) error {
				return errTest
			})

		require.NoError(t, p.Go(context.Background()))
		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, errTest)
	})

	t.Run("pool completion with worker errors", func(t *testing.T) {
		var completeCalled atomic.Bool
		errWorker := errors.New("worker error")

		worker := WorkerFunc[string](func(_ context.Context, _ string) error {
			return errWorker
		})

		p := New[string](1, worker).
			WithPoolCompleteFn(func(context.Context) error {
				completeCalled.Store(true)
				return nil
			})

		require.NoError(t, p.Go(context.Background()))
		p.Submit("test")
		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, errWorker)
		assert.True(t, completeCalled.Load(),
			"pool completion should be called even with worker errors")
	})

	t.Run("chained pools completion order", func(t *testing.T) {
		var order []string
		var mu sync.Mutex
		addToOrder := func(s string) {
			mu.Lock()
			order = append(order, s)
			mu.Unlock()
		}

		// create two pools where first pool completion triggers second pool close
		var p2 *WorkerGroup[string]
		p1 := New[string](2, WorkerFunc[string](func(_ context.Context, _ string) error {
			addToOrder("p1 worker")
			p2.Submit("from p1")
			return nil
		})).WithPoolCompleteFn(func(ctx context.Context) error {
			addToOrder("p1 complete")
			return p2.Close(ctx)
		})

		p2 = New[string](2, WorkerFunc[string](func(_ context.Context, _ string) error {
			addToOrder("p2 worker")
			return nil
		})).WithPoolCompleteFn(func(context.Context) error {
			addToOrder("p2 complete")
			return nil
		})

		require.NoError(t, p1.Go(context.Background()))
		require.NoError(t, p2.Go(context.Background()))

		p1.Submit("test")
		require.NoError(t, p1.Close(context.Background()))

		mu.Lock()
		require.Contains(t, order, "p1 complete")
		require.Contains(t, order, "p2 complete")

		// find indices manually
		p1CompleteIdx := -1
		p2CompleteIdx := -1
		for i, s := range order {
			if s == "p1 complete" {
				p1CompleteIdx = i
			}
			if s == "p2 complete" {
				p2CompleteIdx = i
			}
		}

		assert.Greater(t, p2CompleteIdx, p1CompleteIdx,
			"p1 should complete before p2")
		mu.Unlock()
	})

	t.Run("context cancellation", func(t *testing.T) {
		var completeCalled atomic.Bool
		worker := WorkerFunc[string](func(ctx context.Context, _ string) error {
			// simulate long running work
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
				return nil
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		p := New[string](1, worker).
			WithPoolCompleteFn(func(context.Context) error {
				completeCalled.Store(true)
				return nil
			})

		require.NoError(t, p.Go(ctx))
		p.Submit("test")
		cancel() // cancel before work completes

		err := p.Close(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, completeCalled.Load(),
			"pool completion should not be called on context cancellation")
	})
}

func TestPool_ChainedBatching(t *testing.T) {
	var p1, p2, p3 *WorkerGroup[int]
	var stage1, stage2, stage3 atomic.Int32

	// first stage multiplies by 2
	p1 = New[int](2, WorkerFunc[int](func(_ context.Context, v int) error {
		stage1.Add(1)
		p2.Send(v * 2)
		return nil
	})).WithBatchSize(3).WithPoolCompleteFn(func(ctx context.Context) error {
		return p2.Close(ctx)
	})

	// second stage multiplies by 3
	p2 = New[int](2, WorkerFunc[int](func(_ context.Context, v int) error {
		stage2.Add(1)
		p3.Send(v * 3)
		return nil
	})).WithBatchSize(3).WithPoolCompleteFn(func(ctx context.Context) error {
		return p3.Close(ctx)
	})

	// final stage just counts
	var results []int
	var mu sync.Mutex

	p3 = New[int](2, WorkerFunc[int](func(_ context.Context, v int) error {
		stage3.Add(1)
		mu.Lock()
		results = append(results, v)
		mu.Unlock()
		return nil
	})).WithBatchSize(3)

	require.NoError(t, p1.Go(context.Background()))
	require.NoError(t, p2.Go(context.Background()))
	require.NoError(t, p3.Go(context.Background()))

	// submit items
	inputCount := 100
	for i := range inputCount {
		p1.Submit(i)
	}
	require.NoError(t, p1.Close(context.Background()))

	// verify counts match
	assert.Equal(t, int32(inputCount), stage1.Load(), "stage1 should process all inputs")
	assert.Equal(t, int32(inputCount), stage2.Load(), "stage2 should process all items from stage1")
	assert.Equal(t, int32(inputCount), stage3.Load(), "stage3 should process all items from stage2")

	mu.Lock()
	assert.Len(t, results, inputCount, "should have same number of results as inputs")
	mu.Unlock()
}

func TestPool_HeavyBatching(t *testing.T) {
	var processed atomic.Int32
	var batches atomic.Int32

	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		processed.Add(1)
		batches.Add(1)
		time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond) // simulate work
		return nil
	})

	p := New[int](3, worker).WithBatchSize(10)
	require.NoError(t, p.Go(context.Background()))

	// submit items that should form complete and partial batches
	const items = 1000
	for i := range items {
		p.Submit(i)
	}
	require.NoError(t, p.Close(context.Background()))

	assert.Equal(t, int32(items), processed.Load(), "should process exactly as many items as submitted")

	// log batch stats
	t.Logf("Submitted: %d, Processed: %d, Batches: %d",
		items, processed.Load(), batches.Load())
	t.Logf("Average batch size: %.2f", float64(processed.Load())/float64(batches.Load()))
}

func TestPool_BatchedSend(t *testing.T) {
	var p1, p2 *WorkerGroup[int]
	var stage1, stage2 atomic.Int32
	var processed sync.Map

	// first pool submits to second
	p1 = New[int](2, WorkerFunc[int](func(_ context.Context, v int) error {
		stage1.Add(1)
		time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond)
		p2.Send(v)
		return nil
	})).WithBatchSize(10).WithPoolCompleteFn(func(ctx context.Context) error {
		return p2.Close(ctx)
	})

	// second pool just counts
	p2 = New[int](2, WorkerFunc[int](func(_ context.Context, v int) error {
		stage2.Add(1)
		time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond)
		if _, loaded := processed.LoadOrStore(v, true); loaded {
			t.Errorf("value %d processed more than once", v)
		}
		return nil
	})).WithBatchSize(10)

	require.NoError(t, p1.Go(context.Background()))
	require.NoError(t, p2.Go(context.Background()))

	const items = 1000
	for i := range items {
		p1.Submit(i)
	}
	require.NoError(t, p1.Close(context.Background()))

	// count unique processed items
	var uniqueProcessed int
	processed.Range(func(_, _ any) bool {
		uniqueProcessed++
		return true
	})

	t.Logf("Stage1: %d, Stage2: %d, Unique: %d",
		stage1.Load(), stage2.Load(), uniqueProcessed)
	assert.Equal(t, items, uniqueProcessed, "should process each item exactly once")
	assert.Equal(t, int32(items), stage1.Load(), "stage1 count should match input")
	assert.Equal(t, int32(items), stage2.Load(), "stage2 count should match input")
}

func TestPool_DoubleGoActivation(t *testing.T) {
	worker := WorkerFunc[int](func(context.Context, int) error { return nil })
	p := New[int](2, worker)

	require.NoError(t, p.Go(context.Background()))

	// second Go() call should return error
	err := p.Go(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already activated")

	require.NoError(t, p.Close(context.Background()))
}

func TestPool_NegativeBatchSize(t *testing.T) {
	var processed atomic.Int32
	worker := WorkerFunc[int](func(context.Context, int) error {
		processed.Add(1)
		return nil
	})

	// negative batch size should be treated as 0 (direct mode, no batching)
	p := New[int](2, worker).WithBatchSize(-1)
	require.NoError(t, p.Go(context.Background()))

	p.Submit(1)
	p.Submit(2)
	p.Submit(3)

	require.NoError(t, p.Close(context.Background()))
	assert.Equal(t, int32(3), processed.Load(), "all items should be processed")
}

func TestPool_CloseRespectsContextTimeout(t *testing.T) {
	started := make(chan struct{})
	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		close(started)
		<-ctx.Done() // block until pool context is cancelled
		return ctx.Err()
	})

	// t.Context() is cancelled when test ends, ensuring worker cleanup
	p := New[int](1, worker).WithBatchSize(0)
	require.NoError(t, p.Go(t.Context()))
	p.Submit(1)

	<-started // wait for worker to start processing

	// close with short timeout - should return timeout error, not hang
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Close(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPool_WaitRespectsContextTimeout(t *testing.T) {
	started := make(chan struct{})
	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		close(started)
		<-ctx.Done() // block until pool context is cancelled
		return ctx.Err()
	})

	// t.Context() is cancelled when test ends, ensuring worker cleanup
	p := New[int](1, worker).WithBatchSize(0)
	require.NoError(t, p.Go(t.Context()))
	p.Submit(1)

	<-started // wait for worker to start processing

	// wait with short timeout - should return timeout error, not hang
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Wait(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPool_CloseWithCancelledContext(t *testing.T) {
	worker := WorkerFunc[int](func(context.Context, int) error { return nil })
	p := New[int](1, worker)
	require.NoError(t, p.Go(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := p.Close(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPool_CloseFlushDoesNotDeadlock(t *testing.T) {
	// worker that fails immediately, causing workers to exit
	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		return errors.New("fail immediately")
	})

	p := New[int](1, worker).WithBatchSize(10)
	require.NoError(t, p.Go(context.Background()))

	// submit items but don't fill batch - these sit in accumulator
	for i := range 5 {
		p.Submit(i)
	}

	// close with timeout - should not deadlock trying to flush to dead worker
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := p.Close(ctx)
	// expect error (either from worker failure or timeout), but not hang
	require.Error(t, err)
}

func TestPool_WorkerCompleteFnErrorWithExistingError(t *testing.T) {
	workerErr := errors.New("worker error")
	completeErr := errors.New("complete callback error")

	worker := WorkerFunc[int](func(context.Context, int) error {
		return workerErr
	})

	var completeCalled atomic.Bool
	p := New[int](1, worker).
		WithContinueOnError().
		WithWorkerCompleteFn(func(context.Context, int, Worker[int]) error {
			completeCalled.Store(true)
			return completeErr
		})

	require.NoError(t, p.Go(context.Background()))
	p.Submit(1)
	err := p.Close(context.Background())

	// with continueOnError, we expect to get an error
	require.Error(t, err)
	// worker complete function should have been called
	assert.True(t, completeCalled.Load(), "worker complete function should be called")
}
