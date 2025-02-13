# Task Processor with Middleware - Example

This example demonstrates how to use middleware in [go-pkgz/pool](https://github.com/go-pkgz/pool) package to build a robust task processing system. It shows both built-in middleware usage and custom middleware creation, emphasizing how middleware can add cross-cutting functionality without modifying the core processing logic.

## What Makes it Special?

1. Middleware composition:
    - Shows how multiple middleware work together
    - Demonstrates middleware execution order
    - Combines both built-in and custom middleware

2. Cross-cutting concerns:
    - Input validation before processing
    - Automatic retries for failed tasks
    - Panic recovery for robustness
    - Structured logging for observability

3. Real-world patterns:
    - Configuration management
    - Error handling
    - Metrics collection
    - Structured logging with slog

## Features

- Task validation before processing
- Automatic retries with exponential backoff
- Panic recovery with custom handler
- Structured JSON logging
- Performance metrics collection
- Configurable worker count and retry attempts

## Installation

```bash
go build
```

## Usage

```bash
go run main.go [options]
```

Options:
- `-workers` - number of worker goroutines (default: 2)
- `-retries` - number of retries for failed tasks (default: 3)

Example:
```bash
go run main.go -workers 4 -retries 5
```

## Implementation Details

The implementation demonstrates several key concepts:

1. Middleware creation:
   ```go
   func makeStructuredLogger(logger *slog.Logger) pool.Middleware[Task] {
       return func(next pool.Worker[Task]) pool.Worker[Task] {
           return pool.WorkerFunc[Task](func(ctx context.Context, task Task) error {
               // pre-processing logging
               err := next.Do(ctx, task)
               // post-processing logging
               return err
           })
       }
   }
   ```

2. Middleware composition:
   ```go
   pool.New[Task](workers, makeWorker()).Use(
       middleware.Validate(validator),    // validate first
       middleware.Retry[Task](retries),  // then retry on failure
       middleware.Recovery[Task](handler), // recover from panics
       customLogger,               // log everything
   )
   ```

3. Task processing:
   ```go
   type Task struct {
       ID       string `json:"id"`
       Priority int    `json:"priority"`
       Payload  string `json:"payload"`
   }
   ```

## Output Example

```json
{
    "time": "2025-02-12T10:00:00Z",
    "level": "DEBUG",
    "msg": "processing task",
    "task_id": "1",
    "priority": 1,
    "payload": {"id":"1","priority":1,"payload":"normal task"}
}
{
    "time": "2025-02-12T10:00:00Z",
    "level": "INFO",
    "msg": "task completed",
    "task_id": "1",
    "duration_ms": 100
}
{
    "time": "2025-02-12T10:00:00Z",
    "level": "ERROR",
    "msg": "task failed",
    "task_id": "2",
    "duration_ms": 100,
    "error": "failed to process task 2"
}
```

## Architecture

The program is structured in several logical components:

```
main
  ├── setupConfig    - configuration and logger setup
  ├── makeWorker     - core worker implementation
  ├── makeValidator  - input validation rules
  ├── makePool      - pool creation with middleware
  └── runPool       - execution and task submission
```

Each component is isolated and has a single responsibility, making the code easy to maintain and test.

## Notes

- Middleware executes in the order it's added to Use()
- The first middleware wraps the outermost layer
- Built-in middleware handles common patterns
- Custom middleware can add any functionality
- Structured logging as an example of cross-cutting concern