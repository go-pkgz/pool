package pool

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// task simulates some CPU-bound work
func task(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		sum += i
	}
	return sum
}

// benchTask is a somewhat realistic task that combines CPU work with memory allocation
func benchTask(size int) []int { //nolint:unparam // size is used in the benchmark
	res := make([]int, 0, size)
	for i := 0; i < size; i++ {
		res = append(res, task(1))
	}
	return res
}

func BenchmarkPool(b *testing.B) {
	size, workers, iterations := 1000, 8, 100
	worker := WorkerFunc[int](func(context.Context, int) error {
		benchTask(size)
		return nil
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		p := New[int](workers, worker)
		p.Go(ctx)
		b.StartTimer()

		// sender runs submissions and closes the pool
		go func() {
			for j := 0; j < iterations; j++ {
				p.Submit(j)
			}
			p.Close(ctx) // close after all submissions
		}()

		// main goroutine waits for completion
		p.Wait(ctx)
	}
}

func BenchmarkPoolCompare(b *testing.B) {
	workers := []int{16, 8, 4, 1}
	iterations := 500

	for _, w := range workers {
		prefix := "workers=" + strconv.Itoa(w)

		// Test our pool implementation
		b.Run(prefix+"/pool", func(b *testing.B) {
			worker := WorkerFunc[int](func(context.Context, int) error {
				benchTask(w)
				return nil
			})
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				p := New[int](w, worker).WithWorkerChanSize(100)
				p.Go(ctx)
				b.StartTimer()

				go func() {
					for j := 0; j < iterations; j++ {
						p.Submit(j)
					}
					p.Close(ctx)
				}()
				p.Wait(ctx)
			}
		})

		b.Run(prefix+"/pool-chunked", func(b *testing.B) {
			worker := WorkerFunc[int](func(context.Context, int) error {
				benchTask(w)
				return nil
			})
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				p := New[int](w, worker).WithWorkerChanSize(100).WithChunkFn(func(v int) string {
					return strconv.Itoa(v % w) // distribute by modulo
				})
				p.Go(ctx)
				b.StartTimer()

				go func() {
					for j := 0; j < iterations; j++ {
						p.Submit(j)
					}
					p.Close(ctx)
				}()
				p.Wait(ctx)
			}
		})

		// Test batched pool implementation
		batchSizes := []int{0, 5, 10, 20}
		for _, batchSize := range batchSizes {
			b.Run(fmt.Sprintf("%s/pool-batch-%d", prefix, batchSize), func(b *testing.B) {
				worker := WorkerFunc[int](func(context.Context, int) error {
					benchTask(w)
					return nil
				})
				ctx := context.Background()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					p := New[int](w, worker).
						WithWorkerChanSize(100).
						WithBatchSize(batchSize)
					p.Go(ctx)
					b.StartTimer()

					go func() {
						for j := 0; j < iterations; j++ {
							p.Submit(j)
						}
						p.Close(ctx)
					}()
					p.Wait(ctx)
				}
			})

			// Test batched pool with chunking
			b.Run(fmt.Sprintf("%s/pool-batch-%d-chunked", prefix, batchSize), func(b *testing.B) {
				worker := WorkerFunc[int](func(context.Context, int) error {
					benchTask(w)
					return nil
				})
				ctx := context.Background()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					p := New[int](w, worker).WithWorkerChanSize(100).WithBatchSize(batchSize).WithChunkFn(func(v int) string {
						return strconv.Itoa(v % w)
					})
					p.Go(ctx)
					b.StartTimer()

					go func() {
						for j := 0; j < iterations; j++ {
							p.Submit(j)
						}
						p.Close(ctx)
					}()
					p.Wait(ctx)
				}
			})
		}

		// Test errgroup implementation
		b.Run(prefix+"/errgroup", func(b *testing.B) {
			ctx := context.Background()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				items := make(chan int, iterations)
				g, _ := errgroup.WithContext(ctx)
				g.SetLimit(w)
				b.StartTimer()
				// start workers
				for range w {
					g.Go(func() error {
						for item := range items {
							benchTask(w)
							_ = item
						}
						return nil
					})
				}

				// sender goroutine submits and closes
				go func() {
					for j := 0; j < iterations; j++ {
						items <- j
					}
					close(items)
				}()

				if err := g.Wait(); err != nil {
					b.Fatal(err)
				}
			}
		})

		// Test traditional worker pool
		b.Run(prefix+"/traditional", func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				items := make(chan int, iterations)
				done := make(chan struct{})
				b.StartTimer()

				// start workers
				for range w {
					go func() {
						for item := range items {
							benchTask(w)
							_ = item
						}
						done <- struct{}{}
					}()
				}

				// sender goroutine submits and closes
				go func() {
					for j := 0; j < iterations; j++ {
						items <- j
					}
					close(items)
				}()

				// wait for all workers to complete
				for range w {
					<-done
				}
			}
		})
	}
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
