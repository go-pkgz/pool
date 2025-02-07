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

	p, err := New[string](2, Options[string]().WithWorker(worker))
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
	p, err := New[string](1,
		opts.WithWorker(worker),
		opts.WithBatchSize(batchSize),
	)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	for i := 0; i < 5; i++ {
		p.Submit(fmt.Sprintf("%d", i))
	}

	require.NoError(t, p.Close(context.Background()))

	// verify batches are of correct size (except maybe last one)
	for i, batch := range batches[:len(batches)-1] {
		require.Equal(t, batchSize, len(batch), "batch %d has wrong size", i)
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
	p, err := New[string](2,
		opts.WithWorker(worker),
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

	p, err := New[string](1, Options[string]().WithWorker(worker))
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2") // should not be processed

	err = p.Close(context.Background())
	assert.ErrorIs(t, err, errTest)
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
	p, err := New[string](1,
		opts.WithWorker(worker),
		opts.WithContinueOnError(),
	)
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	p.Submit("ok1")
	p.Submit("error")
	p.Submit("ok2")

	err = p.Close(context.Background())
	assert.ErrorIs(t, err, errTest)
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

	p, err := New[string](1, Options[string]().WithWorker(worker))
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
	p, err := New[string](2,
		opts.WithWorker(worker),
		opts.WithCompleteFn(completeFn),
	)
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
	p, err := New[string](2,
		opts.WithWorkerMaker(workerMaker),
		opts.WithWorkerChanSize(5), // allow more concurrent processing
	)
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

	p, err := New[string](2, Options[string]().WithWorker(worker))
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	// submit in a separate goroutine since we'll use Wait
	go func() {
		inputs := []string{"1", "2", "3"}
		for _, v := range inputs {
			p.Submit(v)
		}
		err := p.Close(context.Background())
		require.NoError(t, err)
	}()

	// wait for completion
	require.NoError(t, p.Wait(context.Background()))

	// verify all items were processed
	mu.Lock()
	require.Equal(t, 3, len(processed))
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

	p, err := New[string](1, Options[string]().WithWorker(worker))
	require.NoError(t, err)
	require.NoError(t, p.Go(context.Background()))

	go func() {
		p.Submit("ok")
		p.Submit("error")
		err := p.Close(context.Background())
		require.Error(t, err)
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

		p, err := New[int](2, Options[int]().WithWorker(worker))
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
		p, err := New[int](2,
			opts.WithWorker(worker),
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

// examples

func Example_basic() {
	// collect output
	var out []string
	var mu sync.Mutex

	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		mu.Lock()
		out = append(out, fmt.Sprintf("processed: %d", v))
		mu.Unlock()
		return nil
	})

	p, _ := New[int](2, Options[int]().WithWorker(worker))
	if err := p.Go(context.Background()); err != nil {
		panic(err) // handle error, don't panic in real code
	}

	// submit work
	p.Submit(1)
	p.Submit(2)
	p.Submit(3)

	_ = p.Close(context.Background())

	// print collected output in sorted order
	sort.Strings(out)
	for _, s := range out {
		fmt.Println(s)
	}

	// Output:
	// processed: 1
	// processed: 2
	// processed: 3
}

func Example_withBatching() {
	// collect output to ensure deterministic order
	var out []string
	var mu sync.Mutex

	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		mu.Lock()
		out = append(out, fmt.Sprintf("batch item: %d", v))
		mu.Unlock()
		return nil
	})

	opts := Options[int]()
	p, _ := New[int](2,
		opts.WithWorker(worker),
		opts.WithBatchSize(2), // process items in batches of 2
	)
	p.Go(context.Background())

	// submit items - they will be processed in batches
	for i := 1; i <= 5; i++ {
		p.Submit(i)
	}

	p.Close(context.Background())

	// print collected output in sorted order
	sort.Strings(out)
	for _, s := range out {
		fmt.Println(s)
	}

	// Output:
	// batch item: 1
	// batch item: 2
	// batch item: 3
	// batch item: 4
	// batch item: 5
}

func Example_withRouting() {
	// collect output for deterministic order
	var out []string
	var mu sync.Mutex

	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		mu.Lock()
		out = append(out, fmt.Sprintf("worker %d got %d", metrics.WorkerID(ctx), v))
		mu.Unlock()
		return nil
	})

	// create pool with chunk function that routes based on even/odd
	opts := Options[int]()
	p, _ := New[int](2,
		opts.WithWorker(worker),
		opts.WithChunkFn(func(v int) string {
			if v%2 == 0 {
				return "even" // this will hash to a consistent worker ID
			}
			return "odd" // this will hash to a different worker ID
		}),
	)
	p.Go(context.Background())

	// Submit in order
	p.Submit(1)
	time.Sleep(time.Millisecond) // ensure ordering for example output
	p.Submit(2)
	time.Sleep(time.Millisecond)
	p.Submit(3)
	time.Sleep(time.Millisecond)
	p.Submit(4)

	p.Close(context.Background())

	// print collected output in original order (no sort)
	for _, s := range out {
		fmt.Println(s)
	}

	// Output:
	// worker 0 got 1
	// worker 1 got 2
	// worker 0 got 3
	// worker 1 got 4
}

func Example_withError() {
	// collect output to ensure deterministic order
	var out []string
	var mu sync.Mutex

	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		if v == 0 {
			return fmt.Errorf("zero value not allowed")
		}
		mu.Lock()
		out = append(out, fmt.Sprintf("processed: %d", v))
		mu.Unlock()
		return nil
	})

	opts := Options[int]()
	p, _ := New[int](1,
		opts.WithWorker(worker),
		opts.WithContinueOnError(), // don't stop on errors
	)
	p.Go(context.Background())

	p.Submit(1)
	p.Submit(0) // this will fail but processing continues
	p.Submit(2)

	err := p.Close(context.Background())
	if err != nil {
		mu.Lock()
		out = append(out, fmt.Sprintf("finished with error: %v", err))
		mu.Unlock()
	}

	// print collected output in sorted order
	sort.Strings(out)
	for _, s := range out {
		fmt.Println(s)
	}

	// Output:
	// finished with error: total errors: 1, last error: worker 0 failed: zero value not allowed
	// processed: 1
	// processed: 2
}

func Example_withContext() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		select {
		case <-time.After(100 * time.Millisecond):
			return nil
		case <-ctx.Done():
			fmt.Println("context cancelled") //nolint
			return ctx.Err()
		}
	})

	p, _ := New[int](1, Options[int]().WithWorker(worker))
	p.Go(ctx)

	p.Submit(1) // this will be interrupted by context timeout

	err := p.Close(context.Background())
	if err != nil {
		fmt.Printf("pool closed with error: %v\n", err)
	}

	// Output:
	// context cancelled
	// pool closed with error: context deadline exceeded
}

func Example_withCollector() {
	type Item struct {
		val   int
		label string
	}

	// create collector for results with buffer size 10
	collector := NewCollector[Item](context.Background(), 10)

	// create worker that processes numbers and sends results to collector
	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		// simulate processing delay for consistent output
		time.Sleep(time.Duration(v) * time.Millisecond)

		result := Item{
			val:   v * 2,  // double the value
			label: "proc", // add label
		}
		collector.Submit(result)
		return nil
	})

	// create and start pool
	p, _ := New[int](2, Options[int]().WithWorker(worker))
	p.Go(context.Background())

	// submit items asynchronously
	go func() {
		for i := 1; i <= 3; i++ {
			p.Submit(i)
		}
		p.Close(context.Background())
		collector.Close() // close collector after pool is done
	}()

	// collect and print results
	results, _ := collector.All()
	for _, res := range results {
		fmt.Printf("got result: %d (%s)\n", res.val, res.label)
	}

	// Output:
	// got result: 2 (proc)
	// got result: 4 (proc)
	// got result: 6 (proc)
}

func Example_withCollectorIterator() {
	collector := NewCollector[string](context.Background(), 5)

	worker := WorkerFunc[int](func(_ context.Context, v int) error {
		time.Sleep(time.Duration(v) * time.Millisecond) // ensure predictable order
		collector.Submit(fmt.Sprintf("value %d", v))
		return nil
	})

	p, _ := New[int](2, Options[int]().WithWorker(worker))
	p.Go(context.Background())

	// submit items asynchronously
	go func() {
		for i := 1; i <= 3; i++ {
			p.Submit(i)
		}
		p.Close(context.Background())
		collector.Close()
	}()

	// use iterator to process results as they arrive
	for val, err := range collector.Iter() {
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		fmt.Printf("processed: %s\n", val)
	}

	// Output:
	// processed: value 1
	// processed: value 2
	// processed: value 3
}

func Example_fibCalculator() {
	// FibResult type to store both input and calculated Fibonacci number
	type FibResult struct {
		n   int
		fib uint64
	}

	// create collector for results
	collector := NewCollector[FibResult](context.Background(), 10)

	// worker calculating fibonacci numbers
	worker := WorkerFunc[int](func(_ context.Context, n int) error {
		if n <= 0 {
			return fmt.Errorf("invalid input: %d", n)
		}

		// calculate fibonacci number
		var a, b uint64 = 0, 1
		for i := 0; i < n; i++ {
			a, b = b, a+b
		}

		collector.Submit(FibResult{n: n, fib: a})
		return nil
	})

	// create pool with 3 workers
	p, _ := New[int](3, Options[int]().WithWorker(worker))
	p.Go(context.Background())

	// submit numbers to calculate asynchronously
	go func() {
		numbers := []int{5, 7, 10, 3, 8}
		for _, n := range numbers {
			p.Submit(n)
		}
		p.Close(context.Background())
		collector.Close()
	}()

	// collect results and sort them by input number for consistent output
	results, _ := collector.All()
	sort.Slice(results, func(i, j int) bool {
		return results[i].n < results[j].n
	})

	// print results
	for _, res := range results {
		fmt.Printf("fib(%d) = %d\n", res.n, res.fib)
	}

	// Output:
	// fib(3) = 2
	// fib(5) = 5
	// fib(7) = 13
	// fib(8) = 21
	// fib(10) = 55
}

func Example_chainedCalculation() {
	// stage 1: calculate fibonacci numbers in parallel
	type FibResult struct {
		n   int
		fib uint64
	}
	stage1Collector := NewCollector[FibResult](context.Background(), 10)

	fibWorker := WorkerFunc[int](func(_ context.Context, n int) error {
		var a, b uint64 = 0, 1
		for i := 0; i < n; i++ {
			a, b = b, a+b
		}
		stage1Collector.Submit(FibResult{n: n, fib: a})
		return nil
	})

	// stage 2: for each fibonacci number, calculate its factors
	type FactorsResult struct {
		n       uint64
		factors []uint64
	}
	stage2Collector := NewCollector[FactorsResult](context.Background(), 10)

	factorsWorker := WorkerFunc[FibResult](func(_ context.Context, res FibResult) error {
		if res.fib <= 1 {
			stage2Collector.Submit(FactorsResult{n: res.fib, factors: []uint64{res.fib}})
			return nil
		}

		var factors []uint64
		n := res.fib
		for i := uint64(2); i*i <= n; i++ {
			for n%i == 0 {
				factors = append(factors, i)
				n /= i
			}
		}
		if n > 1 {
			factors = append(factors, n)
		}

		stage2Collector.Submit(FactorsResult{n: res.fib, factors: factors})
		return nil
	})

	// create and start both pools
	pool1, _ := New[int](3, Options[int]().WithWorker(fibWorker))
	pool1.Go(context.Background())

	pool2, _ := New[FibResult](2, Options[FibResult]().WithWorker(factorsWorker))
	pool2.Go(context.Background())

	// stage 1: submit numbers for fibonacci calculation
	go func() {
		numbers := []int{5, 7, 10}
		for _, n := range numbers {
			pool1.Submit(n)
		}
		pool1.Close(context.Background())
		stage1Collector.Close()
	}()

	// stage 2: take fibonacci results and submit for factorization
	go func() {
		for fibRes, err := range stage1Collector.Iter() {
			if err != nil {
				fmt.Printf("stage 1 error: %v\n", err)
				continue
			}
			pool2.Submit(fibRes)
		}
		pool2.Close(context.Background())
		stage2Collector.Close()
	}()

	// collect and print final results
	results, _ := stage2Collector.All()
	sort.Slice(results, func(i, j int) bool {
		return results[i].n < results[j].n
	})

	for _, res := range results {
		fmt.Printf("number %d has factors %v\n", res.n, res.factors)
	}

	// Output:
	// number 5 has factors [5]
	// number 13 has factors [13]
	// number 55 has factors [5 11]
}
