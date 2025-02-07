package pool

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/go-pkgz/pool/metrics"
)

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
	// collect output with sync.Map for thread safety
	var out sync.Map

	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		out.Store(v, fmt.Sprintf("worker %d got %d", metrics.WorkerID(ctx), v))
		return nil
	})

	// create pool with chunk function that routes based on even/odd
	opts := Options[int]()
	p, _ := New[int](2,
		opts.WithWorker(worker),
		opts.WithChunkFn(func(v int) string {
			if v%2 == 0 {
				return "even"
			}
			return "odd"
		}),
	)
	p.Go(context.Background())

	// Submit all numbers
	for i := 1; i <= 4; i++ {
		p.Submit(i)
	}

	p.Close(context.Background())

	// print in order to ensure deterministic output
	for i := 1; i <= 4; i++ {
		if v, ok := out.Load(i); ok {
			fmt.Println(v)
		}
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
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := WorkerFunc[int](func(ctx context.Context, v int) error {
		close(started) // signal that worker started
		<-ctx.Done()   // wait for cancellation
		return ctx.Err()
	})

	p, _ := New[int](1, Options[int]().WithWorker(worker))
	p.Go(ctx)
	p.Submit(1)

	<-started // wait for worker to start
	cancel()  // cancel context
	err := p.Close(context.Background())
	fmt.Printf("got error: %v\n", err != nil)

	// Output:
	// got error: true
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

	// collect results and sort them for deterministic output
	results, _ := collector.All()
	sort.Slice(results, func(i, j int) bool {
		return results[i].val < results[j].val
	})

	// print sorted results
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

	// collect all values first
	var values []string
	for val, err := range collector.Iter() {
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		values = append(values, val)
	}

	// sort and print values for deterministic output
	sort.Strings(values)
	for _, val := range values {
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
