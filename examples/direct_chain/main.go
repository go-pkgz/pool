package main

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-pkgz/pool"
)

// data types for each stage of processing pipeline.
// each pool transforms data from its input type to output type.
type stringData struct {
	idx  int       // index in the input array
	data string    // input data
	ts   time.Time // timestamp to track processing duration
}

type countData struct {
	idx   int
	count int
	ts    time.Time
}

type multipliedData struct {
	idx   int
	value int
	ts    time.Time
}

type finalData struct {
	idx    int
	result int
}

func ProcessStrings(ctx context.Context, input []string) ([]finalData, error) {
	// declare pools and counters for debugging
	var pCounter *pool.WorkerGroup[stringData]
	var pMulti *pool.WorkerGroup[countData]
	var pSquares *pool.WorkerGroup[multipliedData]
	var submitted, filtered, multiplied, squared atomic.Int64

	collector := pool.NewCollector[finalData](ctx, 10)

	pCounter = pool.New[stringData](8, pool.WorkerFunc[stringData](func(_ context.Context, d stringData) error {
		submitted.Add(1)
		time.Sleep(time.Duration(rand.Intn(1)) * time.Millisecond)
		count := strings.Count(d.data, "a")
		if count > 2 {
			filtered.Add(1)
			// important: we use Send instead of Submit, because we run inside multiple workers
			// and Submit is not thread-safe. Send does the same, just in thread-safe way
			pMulti.Send(countData{idx: d.idx, count: count, ts: d.ts})
		}
		fmt.Printf("counted 'a' in %q -> %d, duration: %v\n", inputStrings[d.idx], count, time.Since(d.ts))
		return nil
	})).WithBatchSize(3).WithPoolCompleteFn(func(ctx context.Context) error {
		return pMulti.Close(ctx)
	})

	pMulti = pool.New[countData](10, pool.WorkerFunc[countData](func(_ context.Context, d countData) error {
		multiplied.Add(1)
		time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
		val := d.count * 10
		fmt.Printf("multiplied: %d -> %d (src: %q, processing time: %v)\n",
			d.count, val, inputStrings[d.idx], time.Since(d.ts))
		pSquares.Send(multipliedData{idx: d.idx, value: val, ts: d.ts})
		return nil
	})).WithBatchSize(3).WithPoolCompleteFn(func(ctx context.Context) error {
		return pSquares.Close(ctx)
	})

	pSquares = pool.New[multipliedData](10, pool.WorkerFunc[multipliedData](func(_ context.Context, d multipliedData) error {
		squared.Add(1)
		val := d.value * d.value
		fmt.Printf("squared: %d -> %d (src: %q, processing time: %v)\n",
			d.value, val, inputStrings[d.idx], time.Since(d.ts))
		time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
		collector.Submit(finalData{idx: d.idx, result: val})
		return nil
	})).WithBatchSize(3).WithPoolCompleteFn(func(ctx context.Context) error {
		collector.Close()
		return nil
	})

	pCounter.Go(ctx)
	pMulti.Go(ctx)
	pSquares.Go(ctx)

	go func() {
		for i := range input {
			for range 100 {
				pCounter.Submit(stringData{idx: i, data: input[i], ts: time.Now()})
				time.Sleep(time.Duration(rand.Intn(1)) * time.Millisecond)
			}
		}
		pCounter.Close(ctx)
	}()

	var results []finalData
	for v := range collector.Iter() {
		results = append(results, v)
	}

	// print debug statistics
	fmt.Printf("\nProcessing statistics:\n")
	fmt.Printf("Total items submitted: %d\n", submitted.Load())
	fmt.Printf("Items passed filter (>2 'a's): %d\n", filtered.Load())
	fmt.Printf("Items multiplied: %d\n", multiplied.Load())
	fmt.Printf("Items squared: %d\n", squared.Load())
	fmt.Printf("Results collected: %d\n", len(results))

	fmt.Printf("\nPool metrics:\ncounter: %s\nmultiplier: %s\nsquares: %s\n",
		pCounter.Metrics().GetStats(), pMulti.Metrics().GetStats(), pSquares.Metrics().GetStats())
	return results, nil
}

// store input array in a global for logging purposes only
var inputStrings []string

func main() {
	inputStrings = []string{
		"banana",
		"alabama",
		"california",
		"canada",
		"australia",
		"alaska",
		"arkansas",
		"arizona",
		"abracadabra",
		"bandanna",
		"barbarian",
		"antarctica",
		"arctic",
		"baccarat",
	}

	res, err := ProcessStrings(context.Background(), inputStrings)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nFinal results:\n")
	for i, v := range res {
		fmt.Printf(" %d src: %q, squared a-count: %d\n", i, inputStrings[v.idx], v.result)
	}
	fmt.Printf("Total: %d\n", len(res))
}
