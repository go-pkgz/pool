// file: examples/middleware/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-pkgz/pool"
	"github.com/go-pkgz/pool/middleware"
)

// Task represents a job to be processed
type Task struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
	Payload  string `json:"payload"`
}

// config holds application configuration
type config struct {
	workers int
	retries int
	logger  *slog.Logger
}

func main() {
	// parse config and setup logger
	cfg := setupConfig()

	// create worker pool
	p := makePool(cfg)

	// start pool and process tasks
	if err := runPool(context.Background(), p, cfg); err != nil {
		cfg.logger.Error("pool finished with error", "error", err)
		os.Exit(1)
	}
}

func setupConfig() config {
	// parse flags
	workers := flag.Int("workers", 2, "number of workers")
	retries := flag.Int("retries", 3, "number of retries")
	flag.Parse()

	// setup structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	return config{
		workers: *workers,
		retries: *retries,
		logger:  logger,
	}
}

func runPool(ctx context.Context, p *pool.WorkerGroup[Task], cfg config) error {
	// start the pool
	if err := p.Go(ctx); err != nil {
		return fmt.Errorf("failed to start pool: %w", err)
	}

	// submit test tasks
	tasks := []Task{
		{ID: "1", Priority: 1, Payload: "normal task"},
		{ID: "2", Priority: 5, Payload: "fail me"}, // this will fail and retry
		{ID: "3", Priority: 2, Payload: "normal task"},
		{ID: "", Priority: 11, Payload: "invalid"}, // this will fail validation
	}

	for _, task := range tasks {
		p.Submit(task)
	}

	// close pool and wait for completion
	if err := p.Close(ctx); err != nil {
		return err
	}

	// print final metrics
	metrics := p.Metrics().GetStats()
	cfg.logger.Info("pool finished", "processed", metrics.Processed, "errors", metrics.Errors,
		"total_time", metrics.TotalTime.String())

	return nil
}

func makePool(cfg config) *pool.WorkerGroup[Task] {
	return pool.New[Task](cfg.workers, makeWorker()).Use(
		middleware.Validator(makeValidator()),            // validate tasks
		middleware.Retry[Task](cfg.retries, time.Second), // retry failed tasks
		middleware.Recovery[Task](func(p interface{}) { // recover from panics
			cfg.logger.Error("panic recovered", "error", fmt.Sprint(p))
		}),
		makeStructuredLogger(cfg.logger), // custom structured logging
	)
}

func makeWorker() pool.Worker[Task] {
	return pool.WorkerFunc[Task](func(ctx context.Context, task Task) error {
		// simulate some work with random failures
		if strings.Contains(task.Payload, "fail") {
			return fmt.Errorf("failed to process task %s", task.ID)
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	})
}

func makeValidator() func(Task) error {
	return func(task Task) error {
		if task.ID == "" {
			return fmt.Errorf("empty task ID")
		}
		if task.Priority < 0 || task.Priority > 10 {
			return fmt.Errorf("invalid priority %d, must be between 0 and 10", task.Priority)
		}
		return nil
	}
}

func makeStructuredLogger(logger *slog.Logger) pool.Middleware[Task] {
	return func(next pool.Worker[Task]) pool.Worker[Task] {
		return pool.WorkerFunc[Task](func(ctx context.Context, task Task) error {
			start := time.Now()
			taskJSON, _ := json.Marshal(task)

			logger.Debug("processing task", "task_id", task.ID, "priority", task.Priority, "payload", string(taskJSON))

			err := next.Do(ctx, task)
			duration := time.Since(start)

			if err != nil {
				logger.Error("task failed", "task_id", task.ID, "duration_ms", duration.Milliseconds(), "error", err.Error())
				return err
			}

			logger.Info("task completed", "task_id", task.ID, "duration_ms", duration.Milliseconds())
			return nil
		})
	}
}
