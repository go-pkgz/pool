package main

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
)

// data types for each stage of processing pipeline.
// each pool transforms data from its input type to output type.
type stringData struct {
	idx int       // index in the input array
	ts  time.Time // timestamp to track processing duration
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

// counterPool demonstrates the first stage of processing.
// each pool type embeds WorkerGroup to handle concurrent processing and Collector to gather results.
type counterPool struct {
	*pool.WorkerGroup[stringData] // worker group processes stringData
	*pool.Collector[countData]    // collector gathers countData
}

// newCounterPool creates a pool that counts 'a' chars in strings.
// demonstrates pool construction pattern: collector -> worker -> pool.
func newCounterPool(ctx context.Context, workers int) *counterPool {
	collector := pool.NewCollector[countData](ctx, workers) // collector to gather results, buffer size == workers
	p := pool.New[stringData](workers, pool.WorkerFunc[stringData](func(_ context.Context, n stringData) error {
		time.Sleep(time.Duration(rand.Intn(500)) * time.Millisecond) // simulate heavy work
		count := strings.Count(inputStrings[n.idx], "a")             // use global var for logging only
		if count > 2 {
			// demonstrates filtering: only strings with >2 'a's passed to the next stage
			collector.Submit(countData{idx: n.idx, count: count, ts: n.ts})
		}
		fmt.Printf("counted 'a' in %q -> %d, duration: %v\n", inputStrings[n.idx], count, time.Since(n.ts))
		return nil
	}))
	return &counterPool{WorkerGroup: p.WithBatchSize(3), Collector: collector}
}

type multiplierPool struct {
	*pool.WorkerGroup[countData]
	*pool.Collector[multipliedData]
}

func newMultiplierPool(ctx context.Context, workers int) *multiplierPool {
	collector := pool.NewCollector[multipliedData](ctx, workers)
	p := pool.New[countData](workers, pool.WorkerFunc[countData](func(_ context.Context, n countData) error {
		time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
		multiplied := n.count * 10 // transform data: multiply by 10
		fmt.Printf("multiplied: %d -> %d (src: %q, processing time: %v)\n",
			n.count, multiplied, inputStrings[n.idx], time.Since(n.ts))
		collector.Submit(multipliedData{idx: n.idx, value: multiplied, ts: n.ts})
		return nil
	}))
	return &multiplierPool{WorkerGroup: p.WithBatchSize(3), Collector: collector}
}

type squarePool struct {
	*pool.WorkerGroup[multipliedData]
	*pool.Collector[finalData]
}

func newSquarePool(ctx context.Context, workers int) *squarePool {
	collector := pool.NewCollector[finalData](ctx, workers)
	p := pool.New[multipliedData](workers, pool.WorkerFunc[multipliedData](func(_ context.Context, n multipliedData) error {
		squared := n.value * n.value
		fmt.Printf("squared: %d -> %d (src: %q, processing time: %v)\n",
			n.value, squared, inputStrings[n.idx], time.Since(n.ts))
		time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
		collector.Submit(finalData{idx: n.idx, result: squared})
		return nil
	}))
	return &squarePool{WorkerGroup: p.WithBatchSize(3), Collector: collector}
}

// ProcessStrings demonstrates chaining multiple pools together to create a processing pipeline.
// Each pool runs concurrently and processes items as they become available from the previous stage.
func ProcessStrings(ctx context.Context, strings []string) ([]finalData, error) {
	// create all pools before starting any processing
	counter := newCounterPool(ctx, 2)
	multiplier := newMultiplierPool(ctx, 4)
	squares := newSquarePool(ctx, 4)

	// start all pools' workers
	// this is non-blocking operation, workers will start processing as soon as items are submitted
	counter.Go(ctx)
	multiplier.Go(ctx)
	squares.Go(ctx)

	// first goroutine feeds input data into the pipeline
	// we use a goroutine to simulate a real-world scenario where data is coming from an external source
	go func() {
		for i := range strings {
			fmt.Printf("submitting: %q\n", strings[i])
			counter.WorkerGroup.Submit(stringData{idx: i, ts: time.Now()})
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
		}
		// close pool and collector when all inputs are submitted
		counter.WorkerGroup.Close(ctx)
		counter.Collector.Close()
	}()

	// organize pipes between pools
	// we use goroutines to communicate between pools in a non-blocking way
	go func() {
		// pipe from counter to multiplier using collector's iterator
		for v := range counter.Iter() { // iter will stop on completion of counter pool
			multiplier.WorkerGroup.Submit(v)
		}
		multiplier.WorkerGroup.Close(ctx)
		multiplier.Collector.Close()
	}()

	go func() {
		// pipe from multiplier to squares
		for v := range multiplier.Iter() { // iter will stop on completion of multiplier pool
			squares.WorkerGroup.Submit(v)
		}
		squares.WorkerGroup.Close(ctx)
		squares.Collector.Close()
	}()

	// collect final results until all work is done
	var results []finalData
	// iter will stop on completion of squares pool which is the last in the chain
	// this is a blocking operation and will return when all pools are done
	// we don't need to wait for each pool to finish explicitly, the iter handles it
	for v := range squares.Iter() {
		results = append(results, v)
	}

	// print metrics showing how each pool performed
	fmt.Printf("\nmetrics:\ncounter: %s\nmultiplier: %s\nsquares: %s\n",
		counter.Metrics().GetStats(), multiplier.Metrics().GetStats(), squares.Metrics().GetStats())
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
	}

	res, err := ProcessStrings(context.Background(), inputStrings)
	if err != nil {
		panic(err)
	}
	fmt.Println("\nFinal results:")
	for _, v := range res {
		fmt.Printf("src: %q, squared a-count: %d\n", inputStrings[v.idx], v.result)
	}
}
