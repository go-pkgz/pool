package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
)

func main() {
	// command line flags
	var (
		workers   = flag.Int("workers", 4, "number of workers")
		batchSize = flag.Int("batch", 100, "batch size")
		file      = flag.String("file", "", "input file to process")
	)
	flag.Parse()

	if *file == "" {
		log.Fatal("file parameter is required")
	}

	// create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// create collector for words
	collector := pool.NewCollector[string](ctx, 1000)

	// create worker function that splits line into words
	worker := pool.WorkerFunc[string](func(ctx context.Context, line string) error {
		// check context before processing
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// split line into words and submit each word
		words := strings.Fields(line)
		for _, word := range words {
			// check context between words
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// clean up the word - remove punctuation, convert to lower case
			word = strings.ToLower(strings.Trim(word, ".,!?()[]{}\"';:"))
			if len(word) <= 3 {
				continue
			}
			collector.Submit(word)
		}
		time.Sleep(10 * time.Millisecond) // simulate slow processing time
		return nil
	})

	// create pool with worker function
	p := pool.New[string](*workers, worker).
		WithBatchSize(*batchSize).
		WithContinueOnError()

	// start the pool
	if err := p.Go(ctx); err != nil {
		log.Fatal(err)
	}

	// read file line by line and submit to pool
	go func() {
		defer p.Close(ctx)

		f, err := os.Open(*file)
		if err != nil {
			log.Printf("failed to open file: %v", err)
			return
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			p.Submit(scanner.Text())
		}

		if err := scanner.Err(); err != nil {
			log.Printf("error reading file: %v", err)
		}
	}()

	// wait for pool to finish
	if err := p.Wait(ctx); err != nil {
		log.Printf("pool error: %v", err)
	}
	collector.Close()

	// count words from collector
	wordCounts := make(map[string]int)
	totalWords := 0
	for word, err := range collector.Iter() {
		if err != nil {
			log.Printf("error collecting word: %v", err)
			continue
		}
		wordCounts[word]++
		totalWords++
	}

	// get pool metrics
	stats := p.Metrics().GetStats()
	fmt.Printf("\nProcessing stats:\n")
	fmt.Printf("Processed lines: %d\n", stats.Processed)
	fmt.Printf("Total words: %d\n", totalWords)
	fmt.Printf("Unique words: %d\n", len(wordCounts))
	fmt.Printf("Errors: %d\n", stats.Errors)
	fmt.Printf("Processing time: %v\n", stats.ProcessingTime)
	fmt.Printf("Total time: %v\n\n", stats.TotalTime)
	fmt.Printf("all stats: %s\n", stats)

	// print top N most common tokens
	const topN = 10
	type wordCount struct {
		word  string
		count int
	}
	counts := make([]wordCount, 0, len(wordCounts))
	for word, count := range wordCounts {
		counts = append(counts, wordCount{word, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		return counts[i].count > counts[j].count
	})

	fmt.Printf("\nTop %d most common words:\n", topN)
	for i, wc := range counts {
		if i >= topN {
			break
		}
		fmt.Printf("%d. %q: %d times\n", i+1, wc.word, wc.count)
	}
}
