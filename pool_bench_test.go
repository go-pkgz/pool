package pool

import (
	"context"
	"os"
	"runtime/pprof"
	"strconv"
	"sync"
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
func benchTask(size int) []int {
	res := make([]int, 0, size)
	for i := 0; i < size; i++ {
		res = append(res, task(20))
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
	sizes := []int{10, 100, 500}
	workers := []int{1, 4, 8}
	iterations := 50

	for _, size := range sizes {
		for _, w := range workers {
			prefix := "size=" + strconv.Itoa(size) + "_workers=" + strconv.Itoa(w)

			// Test our pool implementation
			b.Run(prefix+"/pool", func(b *testing.B) {
				worker := WorkerFunc[int](func(context.Context, int) error {
					benchTask(size)
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
						require.NoError(b, p.Close(ctx))
					}()
					require.NoError(b, p.Wait(ctx))
				}
			})

			b.Run(prefix+"/pool-chunked", func(b *testing.B) {
				worker := WorkerFunc[int](func(context.Context, int) error {
					benchTask(size)
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
								benchTask(size)
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
								benchTask(size)
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
}

// BenchmarkPoolLatency measures latency with concurrent submitters
func BenchmarkPoolLatency(b *testing.B) {
	sizes := []int{10, 100}
	workers := []int{1, 4}
	loads := []int{10, 100}
	iterations := 100

	for _, size := range sizes {
		for _, w := range workers {
			for _, load := range loads {
				prefix := "size=" + strconv.Itoa(size) + "_workers=" + strconv.Itoa(w) + "_load=" + strconv.Itoa(load)

				// Pool implementation
				b.Run(prefix+"/pool", func(b *testing.B) {
					worker := WorkerFunc[int](func(context.Context, int) error {
						benchTask(size)
						return nil
					})

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						p := New[int](w, worker)
						p.Go(context.Background())

						var wg sync.WaitGroup
						for j := 0; j < load; j++ {
							wg.Add(1)
							go func(id int) {
								defer wg.Done()
								k := iterations / load
								start := id * k
								for n := 0; n < k; n++ {
									p.Submit(start + n)
								}
							}(j)
						}

						wg.Wait()
						p.Close(context.Background())
					}
				})

				// errgroup implementation
				b.Run(prefix+"/errgroup", func(b *testing.B) {
					for i := 0; i < b.N; i++ {
						g, _ := errgroup.WithContext(context.Background())
						items := make(chan int, iterations)

						// Start workers
						for j := 0; j < w; j++ {
							g.Go(func() error {
								for range items {
									benchTask(size)
								}
								return nil
							})
						}

						// Start submitters
						var wg sync.WaitGroup
						for j := 0; j < load; j++ {
							wg.Add(1)
							go func(id int) {
								defer wg.Done()
								k := iterations / load
								start := id * k
								for n := 0; n < k; n++ {
									items <- start + n
								}
							}(j)
						}

						wg.Wait()
						close(items)
						g.Wait()
					}
				})

				// Traditional implementation
				b.Run(prefix+"/traditional", func(b *testing.B) {
					for i := 0; i < b.N; i++ {
						var wg sync.WaitGroup
						items := make(chan int, iterations)

						// Start workers
						for j := 0; j < w; j++ {
							wg.Add(1)
							go func() {
								defer wg.Done()
								for range items {
									benchTask(size)
								}
							}()
						}

						// Start submitters
						var submitWg sync.WaitGroup
						for j := 0; j < load; j++ {
							submitWg.Add(1)
							go func(id int) {
								defer submitWg.Done()
								k := iterations / load
								start := id * k
								for n := 0; n < k; n++ {
									items <- start + n
								}
							}(j)
						}

						submitWg.Wait()
						close(items)
						wg.Wait()
					}
				})
			}
		}
	}
}

func TestPoolWithProfiling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping profiling test in short mode")
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
	iterations := 10000
	ctx := context.Background()
	worker := WorkerFunc[int](func(context.Context, int) error {
		time.Sleep(time.Microsecond) // simulate some work
		return nil
	})

	// test pool implementation
	p := New[int](4, worker)
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
