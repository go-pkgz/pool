package pool

import (
	"context"
	"os"
	"runtime/pprof"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// benchTask is a somewhat realistic task that combines CPU work with memory allocation
func benchTask(size int) []int { //nolint:unparam // size is used in the benchmark
	task := func(n int) int { // simulate some CPU work
		sum := 0
		for i := 0; i < n; i++ {
			sum += i
		}
		return sum
	}
	res := make([]int, 0, size)
	for i := 0; i < size; i++ {
		res = append(res, task(1))
	}
	return res
}

func TestPoolPerf(t *testing.T) {
	n := 1000
	ctx := context.Background()

	t.Run("errgroup", func(t *testing.T) {
		var count2 int32
		st := time.Now()
		defer func() {
			t.Logf("elapsed errgroup: %v", time.Since(st))
		}()
		g, _ := errgroup.WithContext(ctx)
		g.SetLimit(8)
		for i := 0; i < 1000000; i++ {
			g.Go(func() error {
				benchTask(n)
				atomic.AddInt32(&count2, 1)
				return nil
			})
		}
		require.NoError(t, g.Wait())
		assert.Equal(t, int32(1000000), atomic.LoadInt32(&count2))
	})

	t.Run("pool default", func(t *testing.T) {
		// pool with 8 workers
		var count1 int32
		worker := WorkerFunc[int](func(context.Context, int) error {
			benchTask(n)
			atomic.AddInt32(&count1, 1)
			return nil
		})

		st := time.Now()
		p := New[int](8, worker)
		require.NoError(t, p.Go(ctx))
		go func() {
			for i := 0; i < 1000000; i++ {
				p.Submit(i)
			}
			assert.NoError(t, p.Close(ctx))
		}()
		require.NoError(t, p.Wait(ctx))
		assert.Equal(t, int32(1000000), atomic.LoadInt32(&count1))
		t.Logf("elapsed pool: %v", time.Since(st))
	})

	t.Run("pool with 100 chan size", func(t *testing.T) {
		// pool with 8 workers
		var count1 int32
		worker := WorkerFunc[int](func(context.Context, int) error {
			benchTask(n)
			atomic.AddInt32(&count1, 1)
			return nil
		})

		st := time.Now()
		p := New[int](8, worker).WithWorkerChanSize(100)
		require.NoError(t, p.Go(ctx))
		go func() {
			for i := 0; i < 1000000; i++ {
				p.Submit(i)
			}
			assert.NoError(t, p.Close(ctx))
		}()
		require.NoError(t, p.Wait(ctx))
		assert.Equal(t, int32(1000000), atomic.LoadInt32(&count1))
		t.Logf("elapsed pool: %v", time.Since(st))
	})

	t.Run("pool with 100 chan size and 100 batch size", func(t *testing.T) {
		// pool with 8 workers
		var count1 int32
		worker := WorkerFunc[int](func(context.Context, int) error {
			benchTask(n)
			atomic.AddInt32(&count1, 1)
			return nil
		})

		st := time.Now()
		p := New[int](8, worker).WithWorkerChanSize(100).WithBatchSize(100)
		require.NoError(t, p.Go(ctx))
		go func() {
			for i := 0; i < 1000000; i++ {
				p.Submit(i)
			}
			assert.NoError(t, p.Close(ctx))
		}()
		require.NoError(t, p.Wait(ctx))
		assert.Equal(t, int32(1000000), atomic.LoadInt32(&count1))
		t.Logf("elapsed pool: %v", time.Since(st))
	})

	t.Run("pool with 100 chan size and 100 batch size and chunking", func(t *testing.T) {
		// pool with 8 workers
		var count1 int32
		worker := WorkerFunc[int](func(context.Context, int) error {
			benchTask(n)
			atomic.AddInt32(&count1, 1)
			return nil
		})

		st := time.Now()
		p := New[int](8, worker).WithWorkerChanSize(100).WithBatchSize(100).WithChunkFn(func(v int) string {
			return strconv.Itoa(v % 8) // distribute by modulo
		})
		require.NoError(t, p.Go(ctx))
		go func() {
			for i := 0; i < 1000000; i++ {
				p.Submit(i)
			}
			assert.NoError(t, p.Close(ctx))
		}()
		require.NoError(t, p.Wait(ctx))
		assert.Equal(t, int32(1000000), atomic.LoadInt32(&count1))
		t.Logf("elapsed pool: %v", time.Since(st))
	})

}

func BenchmarkPoolCompare(b *testing.B) {
	iterations := 5000
	workers := 16
	n := 5000

	ctx := context.Background()
	worker := WorkerFunc[int](func(context.Context, int) error {
		benchTask(n)
		return nil
	})

	b.Run("pool with chan=100", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p := New[int](workers, worker).WithWorkerChanSize(100)
			p.Go(ctx)

			go func() {
				for j := range iterations {
					p.Submit(j)
				}
				p.Close(ctx)
			}()
			p.Wait(ctx)
		}
	})

	b.Run("pool-chunked", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p := New[int](workers, worker).WithWorkerChanSize(100).WithChunkFn(func(v int) string {
				return strconv.Itoa(v % (v + 1)) // distribute by modulo
			})
			p.Go(ctx)

			go func() {
				for j := range iterations {
					p.Submit(j)
				}
				p.Close(ctx)
			}()
			p.Wait(ctx)
		}
	})

	b.Run("pool-batched", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p := New[int](workers, worker).WithWorkerChanSize(100).WithBatchSize(100)
			p.Go(ctx)

			go func() {
				for j := range iterations {
					p.Submit(j)
				}
				p.Close(ctx)
			}()
			p.Wait(ctx)
		}
	})

	// Test errgroup implementation
	b.Run("errgroup", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			g, _ := errgroup.WithContext(ctx)
			g.SetLimit(workers)

			for range iterations {
				g.Go(func() error {
					benchTask(n)
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestPoolWithProfiling(t *testing.T) {
	// run only if env PROFILING is set
	if os.Getenv("PROFILING") == "" {
		t.Skip("skipping profiling test; set PROFILING to run")
	}

	// start CPU profile
	cpuFile, err := os.Create("cpu.prof")
	require.NoError(t, err)
	defer cpuFile.Close()
	require.NoError(t, pprof.StartCPUProfile(cpuFile))
	defer pprof.StopCPUProfile()

	// create memory profile
	memFile, err := os.Create("mem.prof")
	require.NoError(t, err)
	defer memFile.Close()

	// run pool test
	iterations := 100000
	ctx := context.Background()
	worker := WorkerFunc[int](func(context.Context, int) error {
		benchTask(30000)
		return nil
	})

	// test pool implementation
	p := New[int](4, worker).WithWorkerChanSize(100)
	require.NoError(t, p.Go(ctx))

	done := make(chan struct{})
	go func() {
		for i := 0; i < iterations; i++ {
			p.Submit(i)
		}
		p.Close(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// create memory profile after test
	require.NoError(t, pprof.WriteHeapProfile(memFile))
}
