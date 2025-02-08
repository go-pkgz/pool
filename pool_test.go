package pool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
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

	p, err := New[string](2, worker)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	inputs := []string{"1", "2", "3", "4", "5"}
	for _, v := range inputs {
		p.Submit(v)
	}

	require.NoError(t, p.Close(context.Background()))

	sort.Strings(processed)
	assert.Equal(t, inputs, processed)
}

func TestPool_Batching(t *testing.T) {
	var batches [][]string
	var mu sync.Mutex

	batchSize := 2
	worker := WorkerFunc[string](func(_ context.Context, v string) error {
		mu.Lock()
		if len(batches) == 0 || len(batches[len(batches)-1]) >= batchSize {
			batches = append(batches, []string{})
		}
		batches[len(batches)-1] = append(batches[len(batches)-1], v)
		mu.Unlock()
		return nil
	})

	opts := Options[string]()
	p, err := New[string](1, worker, opts.WithBatchSize(batchSize))
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	for i := 0; i < 5; i++ {
		p.Submit(fmt.Sprintf("%d", i))
	}

	require.NoError(t, p.Close(context.Background()))

	// verify batches are of correct size (except maybe last one)
	for i, batch := range batches[:len(batches)-1] {
		require.Len(t, batch, batchSize, "batch %d has wrong size", i)
	}
	assert.LessOrEqual(t, len(batches[len(batches)-1]), batchSize)
}

func TestPool_ChunkDistribution(t *testing.T) {
	var workerCounts [2]int32

	worker := WorkerFunc[string](func(ctx context.Context, _ string) error {
		id := metrics.WorkerID(ctx)
		atomic.AddInt32(&workerCounts[id], 1)
		return nil
	})

	opts := Options[string]()
	p, err := New[string](2, worker,
		opts.WithChunkFn(func(v string) string { return v }),
	)
	require.NoError(t, err)
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

	p, err := New[string](1, worker)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2") // should not be processed

	err = p.Close(context.Background())
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

	opts := Options[string]()
	p, err := New[string](1, worker, opts.WithContinueOnError())
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2")

	err = p.Close(context.Background())
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

	p, err := New[string](1, worker)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NoError(t, p.Go(ctx))
	p.Submit("test")

	err = p.Close(context.Background())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPool_WorkerCompletion(t *testing.T) {
	var completedWorkers []int
	var mu sync.Mutex

	worker := WorkerFunc[string](func(_ context.Context, _ string) error { return nil })
	completeFn := func(_ context.Context, id int, _ Worker[string]) error {
		mu.Lock()
		completedWorkers = append(completedWorkers, id)
		mu.Unlock()
		return nil
	}

	opts := Options[string]()
	p, err := New[string](2, worker, opts.WithCompleteFn(completeFn))
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))
	require.NoError(t, p.Close(context.Background()))

	sort.Ints(completedWorkers)
	assert.Equal(t, []int{0, 1}, completedWorkers)
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

	opts := Options[string]()
	p, err := NewStateful[string](2, workerMaker, opts.WithWorkerChanSize(5))
	require.NoError(t, err)
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

	p, err := New[string](2, worker)
	require.NoError(t, err)
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
	assert.Len(t, processed, 3)
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

	p, err := New[string](1, worker)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	go func() {
		p.Submit("ok")
		p.Submit("error")
		err := p.Close(context.Background())
		assert.Error(t, err)
	}()

	err = p.Wait(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errTest)
}

func TestPool_Distribution(t *testing.T) {
	t.Run("random distribution", func(t *testing.T) {
		var counts [2]int32
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			atomic.AddInt32(&counts[metrics.WorkerID(ctx)], 1)
			return nil
		})

		p, err := New[int](2, worker)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		const n = 10000
		for i := 0; i < n; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		// check distribution, should be roughly equal
		diff := math.Abs(float64(counts[0]-counts[1])) / float64(n)
		require.Less(t, diff, 0.1, "distribution difference %v should be less than 10%%", diff)
		t.Logf("workers distribution: %v, difference: %.2f%%", counts, diff*100)
	})

	t.Run("chunked distribution", func(t *testing.T) {
		var counts [2]int32
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			atomic.AddInt32(&counts[metrics.WorkerID(ctx)], 1)
			return nil
		})

		opts := Options[int]()
		p, err := New[int](2, worker,
			opts.WithChunkFn(func(v int) string {
				return fmt.Sprintf("key-%d", v%10) // 10 different keys
			}),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		const n = 10000
		for i := 0; i < n; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		// check distribution, should be roughly equal
		diff := math.Abs(float64(counts[0]-counts[1])) / float64(n)
		require.Less(t, diff, 0.1, "distribution difference %v should be less than 10%%", diff)
		t.Logf("workers distribution: %v, difference: %.2f%%", counts, diff*100)
	})
}

func TestPool_Metrics(t *testing.T) {
	t.Run("basic metrics", func(t *testing.T) {
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			m := metrics.Get(ctx)
			procEnd := m.StartTimer(metrics.DurationProc)
			time.Sleep(time.Millisecond)
			procEnd()
			m.Inc(metrics.CountProcessed)
			m.Add("custom", 2)
			return nil
		})

		p, err := New[int](2, worker)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		for i := 0; i < 10; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		m := p.Metrics()
		assert.Equal(t, 10, m.Get(metrics.CountProcessed))
		assert.Greater(t, m.GetDuration(metrics.DurationProc), time.Duration(0))
		assert.Equal(t, 0, m.Get(metrics.CountErrors))
		assert.Equal(t, 20, m.Get("custom"))
	})

	t.Run("metrics with errors", func(t *testing.T) {
		worker := WorkerFunc[int](func(ctx context.Context, v int) error {
			m := metrics.Get(ctx)
			m.Inc(metrics.CountProcessed)
			if v%2 == 0 {
				return errors.New("even number")
			}
			return nil
		})

		p, err := New[int](2, worker,
			Options[int]().WithContinueOnError(),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		for i := 0; i < 10; i++ {
			p.Submit(i)
		}
		require.Error(t, p.Close(context.Background()))

		m := p.Metrics()
		assert.Equal(t, 10, m.Get(metrics.CountProcessed))
		assert.Equal(t, 5, m.Get(metrics.CountErrors))
	})

	t.Run("metrics with batching", func(t *testing.T) {
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			m := metrics.Get(ctx)
			m.Inc(metrics.CountProcessed)
			m.Add("custom", 2)
			return nil
		})

		p, err := New[int](2, worker,
			Options[int]().WithBatchSize(3),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		for i := 0; i < 10; i++ {
			p.Submit(i)
		}
		require.NoError(t, p.Close(context.Background()))

		m := p.Metrics()
		assert.Equal(t, 10, m.Get(metrics.CountProcessed))
		assert.Equal(t, 20, m.Get("custom"))
	})

	t.Run("metrics timing", func(t *testing.T) {
		const processingTime = 10 * time.Millisecond
		worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
			m := metrics.Get(ctx)
			procEnd := m.StartTimer(metrics.DurationProc)
			time.Sleep(processingTime)
			procEnd()
			m.Inc(metrics.CountProcessed)
			return nil
		})

		p, err := New[int](2, worker)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		p.Submit(1)
		p.Submit(2)
		require.NoError(t, p.Close(context.Background()))

		m := p.Metrics()
		assert.Equal(t, 2, m.Get(metrics.CountProcessed))
		assert.GreaterOrEqual(t, m.GetDuration(metrics.DurationProc), time.Millisecond*20)
		assert.Less(t, m.GetDuration(metrics.DurationProc), time.Millisecond*30)
		assert.Greater(t, m.GetDuration(metrics.DurationInit), time.Duration(0))
		assert.Greater(t, m.GetDuration(metrics.DurationWrap), time.Duration(0))
	})
}

func TestPool_MetricsAsStruct(t *testing.T) {
	var processed int32
	worker := WorkerFunc[int](func(ctx context.Context, _ int) error {
		atomic.AddInt32(&processed, 1) // track actual processing count
		m := metrics.Get(ctx)
		procEnd := m.StartTimer(metrics.DurationProc)
		time.Sleep(time.Millisecond)
		procEnd()
		m.Inc(metrics.CountProcessed)
		return nil
	})

	p, err := New[int](2, worker)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	// submit 3 items
	p.Submit(1)
	p.Submit(2)
	p.Submit(3)
	require.NoError(t, p.Close(context.Background()))

	stats := p.Metrics().Stats()
	t.Logf("Stats: %+v", stats)
	t.Logf("Actual processed: %d", atomic.LoadInt32(&processed))

	// verify counts
	assert.Equal(t, 3, stats.Processed)
	assert.Equal(t, 0, stats.Errors)
	assert.Equal(t, 0, stats.Dropped)

	// verify timings
	assert.Greater(t, stats.ProcessingTime, time.Duration(0))
	assert.Greater(t, stats.WaitTime, time.Duration(0))
	assert.Greater(t, stats.InitTime, time.Duration(0))
	assert.Greater(t, stats.WrapTime, time.Duration(0))
	assert.Greater(t, stats.TotalTime, time.Duration(0))

	// verify actual vs reported processing
	assert.Equal(t, int(processed), stats.Processed, "processed count mismatch")
}

func TestPool_FinalizeWorker(t *testing.T) {
	t.Run("batch processing with errors", func(t *testing.T) {
		var processed []string
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "error" {
				return fmt.Errorf("test error")
			}
			processed = append(processed, v)
			return nil
		})

		p, err := New[string](1, worker)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		// fill batch buffer with items including error
		p.buf[0] = []string{"ok1", "error", "ok2"}

		// should process until error
		err = p.finalizeWorker(context.Background(), 0, worker)
		require.Error(t, err)
		require.Contains(t, err.Error(), "test error")
		assert.Equal(t, []string{"ok1"}, processed)
	})

	t.Run("batch processing continues on error", func(t *testing.T) {
		var processed []string
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			if v == "error" {
				return fmt.Errorf("test error")
			}
			processed = append(processed, v)
			return nil
		})

		p, err := New[string](1, worker,
			Options[string]().WithContinueOnError(),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		// fill batch buffer with items including error
		p.buf[0] = []string{"ok1", "error", "ok2"}

		// should process all items
		err = p.finalizeWorker(context.Background(), 0, worker)
		require.NoError(t, err)
		assert.Equal(t, []string{"ok1", "ok2"}, processed)
	})

	t.Run("completeFn error", func(t *testing.T) {
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return nil
		})

		completeFnError := fmt.Errorf("complete error")
		p, err := New[string](1, worker,
			Options[string]().WithCompleteFn(func(context.Context, int, Worker[string]) error {
				return completeFnError
			}),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		err = p.finalizeWorker(context.Background(), 0, worker)
		require.Error(t, err)
		require.ErrorIs(t, err, completeFnError)
	})

	t.Run("batch error prevents completeFn", func(t *testing.T) {
		var completeFnCalled bool
		worker := WorkerFunc[string](func(_ context.Context, v string) error {
			return fmt.Errorf("batch error")
		})

		p, err := New[string](1, worker,
			Options[string]().WithCompleteFn(func(context.Context, int, Worker[string]) error {
				completeFnCalled = true
				return fmt.Errorf("complete error")
			}),
		)
		require.NoError(t, err)
		require.NoError(t, p.Go(context.Background()))

		// fill batch buffer with an item
		p.buf[0] = []string{"task"}

		err = p.finalizeWorker(context.Background(), 0, worker)
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
		p, err := New[string](1, worker)
		require.NoError(t, err)
		require.NoError(t, p.Go(ctx))

		// fill batch buffer
		p.buf[0] = []string{"task1", "task2"}

		// cancel context before finalization
		cancel()

		err = p.finalizeWorker(ctx, 0, worker)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}
