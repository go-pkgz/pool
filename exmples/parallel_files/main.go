package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
	"github.com/go-pkgz/pool/metrics"
)

// chunk represents a piece of file to process
type chunk struct {
	data []byte
}

// fileWorker counts words in chunks
type fileWorker struct {
	words     map[string]int
	byteCount int64
}

// Do implements pool.Worker interface
func (w *fileWorker) Do(ctx context.Context, c chunk) error {
	scanner := bufio.NewScanner(strings.NewReader(string(c.data)))
	scanner.Split(bufio.ScanWords)
	m := metrics.Get(ctx)
	for scanner.Scan() {
		word := strings.ToLower(strings.Trim(scanner.Text(), ".,!?()[]{}\"';:"))
		if len(word) > 3 {
			w.words[word]++
			m.Inc("long words")
		} else {
			m.Inc("short words")
		}
	}
	w.byteCount += int64(len(c.data))
	return scanner.Err()
}

func main() {
	var (
		dir      = flag.String("dir", ".", "directory to process")
		workers  = flag.Int("workers", 4, "number of workers")
		pattern  = flag.String("pattern", "*.txt", "file pattern to match")
		topWords = flag.Int("top", 10, "number of top words to show")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	collector := pool.NewCollector[fileWorker](ctx, *workers)

	p := pool.NewStateful[chunk](*workers, func() pool.Worker[chunk] {
		return &fileWorker{words: make(map[string]int)} // create new worker with empty words map
	})

	// set batch size and complete function
	p = p.WithBatchSize(100).WithCompleteFn(func(_ context.Context, _ int, w pool.Worker[chunk]) error {
		collector.Submit(*w.(*fileWorker))
		return nil
	})

	// start pool processing
	if err := p.Go(ctx); err != nil {
		log.Fatal(err)
	}

	// process files
	err := filepath.Walk(*dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if matched, err := filepath.Match(*pattern, filepath.Base(path)); err != nil || !matched {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open %s: %w", path, err)
		}
		defer file.Close()

		buffer := make([]byte, 32*1024)
		for {
			n, err := file.Read(buffer)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("error reading %s: %w", path, err)
			}

			data := make([]byte, n)
			copy(data, buffer[:n])
			p.Submit(chunk{data: data}) // submit chunk to pool
		}
		return nil
	})
	if err != nil {
		log.Printf("error walking files: %v", err)
	}

	// close pool and collector, initiate all data sent and no more data expected
	if err := p.Close(ctx); err != nil {
		log.Printf("pool close error: %v", err)
	}
	collector.Close()

	// merge and print results
	totalWords := make(map[string]int)
	var totalBytes int64

	// iterate over collector results, merge words and count bytes
	for worker := range collector.Iter() {
		for word, count := range worker.words {
			totalWords[word] += count
		}
		totalBytes += worker.byteCount
	}

	fmt.Printf("\nProcessing statistics: %+v\n", p.Metrics().GetStats())
	fmt.Printf("Total bytes: %d\n", totalBytes)
	fmt.Printf("Unique words: %d\n", len(totalWords))
	fmt.Printf("Short words: %d\n", p.Metrics().Get("short words"))
	fmt.Printf("Long words: %d\n", p.Metrics().Get("long words"))

	// prepare sorted list of words
	type wordCount struct {
		word  string
		count int
	}
	counts := make([]wordCount, 0, len(totalWords))
	for word, count := range totalWords {
		counts = append(counts, wordCount{word, count})
	}

	sort.Slice(counts, func(i, j int) bool {
		return counts[i].count > counts[j].count
	})

	fmt.Printf("\nTop %d words:\n", *topWords)
	for i := 0; i < *topWords && i < len(counts); i++ {
		fmt.Printf("%d. %q: %d times\n", i+1, counts[i].word, counts[i].count)
	}
}
