// Package metrics provides a way to collect metrics in a thread-safe way
package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type contextKey string

// TimerType is a type of timer to measure
type TimerType int

const (
	metricsContextKey contextKey = "metrics"
	widContextKey     contextKey = "worker-id"
)

// Timer types
const (
	TimerProc TimerType = iota // processing time
	TimerWait                  // wait time
	TimerInit                  // initialization time
	TimerWrap                  // wrap-up time
)

// Value holds both per-worker stats and shared user stats
type Value struct {
	startTime time.Time

	// per worker stats, no lock needed as each worker uses its own stats
	workerStats []Stats

	// shared user stats protected by mutex
	mu       sync.RWMutex
	userData map[string]int
}

// Stats represents worker-specific metrics with derived values
type Stats struct {
	// raw counters
	Processed int
	Errors    int
	Dropped   int

	// timing
	ProcessingTime time.Duration
	WaitTime       time.Duration
	InitTime       time.Duration
	WrapTime       time.Duration
	TotalTime      time.Duration

	// derived stats, calculated on GetStats
	RatePerSec  float64       // items processed per second
	AvgLatency  time.Duration // average processing time per item
	ErrorRate   float64       // portion of errors
	DroppedRate float64       // portion of dropped items
	Utilization float64       // portion of time spent processing vs waiting
}

// String returns stats info formatted as string
// String returns stats info formatted as string
func (s Stats) String() string {
	var metrics []string

	if s.Processed > 0 {
		metrics = append(metrics, fmt.Sprintf("processed:%d", s.Processed))
		// only add rate and latency if they are non-zero
		if s.RatePerSec > 0 {
			metrics = append(metrics, fmt.Sprintf("rate:%.1f/s", s.RatePerSec))
		}
		if s.AvgLatency > 0 {
			metrics = append(metrics, fmt.Sprintf("avg_latency:%v", s.AvgLatency.Round(time.Millisecond)))
		}
	}
	if s.Errors > 0 {
		if s.ErrorRate > 0 {
			metrics = append(metrics, fmt.Sprintf("errors:%d (%.1f%%)", s.Errors, s.ErrorRate*100)) //nolint:mnd // 100 is not magic number
		} else {
			metrics = append(metrics, fmt.Sprintf("errors:%d", s.Errors))
		}
	}
	if s.Dropped > 0 {
		if s.DroppedRate > 0 {
			metrics = append(metrics, fmt.Sprintf("dropped:%d (%.1f%%)", s.Dropped, s.DroppedRate*100)) //nolint:mnd // 100 is not magic
		} else {
			metrics = append(metrics, fmt.Sprintf("dropped:%d", s.Dropped))
		}
	}
	if s.ProcessingTime > 0 {
		metrics = append(metrics, fmt.Sprintf("proc:%v", s.ProcessingTime.Round(time.Millisecond)))
	}
	if s.WaitTime > 0 {
		metrics = append(metrics, fmt.Sprintf("wait:%v", s.WaitTime.Round(time.Millisecond)))
	}
	if s.InitTime > 0 {
		metrics = append(metrics, fmt.Sprintf("init:%v", s.InitTime.Round(time.Millisecond)))
	}
	if s.WrapTime > 0 {
		metrics = append(metrics, fmt.Sprintf("wrap:%v", s.WrapTime.Round(time.Millisecond)))
	}
	if s.TotalTime > 0 {
		metrics = append(metrics, fmt.Sprintf("total:%v", s.TotalTime.Round(time.Millisecond)))
		if s.Utilization > 0 {
			metrics = append(metrics, fmt.Sprintf("utilization:%.1f%%", s.Utilization*100)) //nolint:mnd // 100 is not magic number
		}
	}

	if len(metrics) > 0 {
		return fmt.Sprintf("[%s]", strings.Join(metrics, ", "))
	}
	return ""
}

// New makes thread-safe metrics collector with specified number of workers
func New(workers int) *Value {
	return &Value{
		startTime:   time.Now(),
		workerStats: make([]Stats, workers),
		userData:    make(map[string]int),
	}
}

// Add increments value for a given key and returns new value
func (m *Value) Add(key string, delta int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userData[key] += delta
	return m.userData[key]
}

// Inc increments value for given key by one
func (m *Value) Inc(key string) int {
	return m.Add(key, 1)
}

// Get returns value for given key from shared stats
func (m *Value) Get(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userData[key]
}

// StartTimer returns a function that when called will record the duration in worker stats
func (m *Value) StartTimer(wid int, t TimerType) func() {
	start := time.Now()
	stats := &m.workerStats[wid]

	return func() {
		duration := time.Since(start)
		switch t {
		case TimerProc:
			stats.ProcessingTime += duration
		case TimerWait:
			stats.WaitTime += duration
		case TimerInit:
			stats.InitTime += duration
		case TimerWrap:
			stats.WrapTime += duration
		}
	}
}

// AddWaitTime adds wait time directly to worker stats
func (m *Value) AddWaitTime(wid int, d time.Duration) {
	m.workerStats[wid].WaitTime += d
}

// IncProcessed increments processed count for worker
func (m *Value) IncProcessed(wid int) {
	m.workerStats[wid].Processed++
}

// IncErrors increments errors count for worker
func (m *Value) IncErrors(wid int) {
	m.workerStats[wid].Errors++
}

// IncDropped increments dropped count for worker
func (m *Value) IncDropped(wid int) {
	m.workerStats[wid].Dropped++
}

// GetStats returns combined stats from all workers
func (m *Value) GetStats() Stats {
	var result Stats

	// sum up stats from all workers
	for i := range m.workerStats {
		result.Processed += m.workerStats[i].Processed
		result.Errors += m.workerStats[i].Errors
		result.Dropped += m.workerStats[i].Dropped

		// sum wait time - represents total idle time across all workers
		result.WaitTime += m.workerStats[i].WaitTime

		// for processing time we take max since workers run in parallel
		result.ProcessingTime = max(result.ProcessingTime, m.workerStats[i].ProcessingTime)

		// sum initialization and wrap times as they are sequential
		result.InitTime += m.workerStats[i].InitTime
		result.WrapTime += m.workerStats[i].WrapTime
	}

	result.TotalTime = time.Since(m.startTime)

	// calculate derived stats
	if result.TotalTime > 0 {
		result.RatePerSec = float64(result.Processed) / result.TotalTime.Seconds()
	}
	if result.Processed > 0 {
		// for average latency we use max processing time divided by total processed
		result.AvgLatency = result.ProcessingTime / time.Duration(result.Processed)
	}
	totalAttempted := result.Processed + result.Errors + result.Dropped
	if totalAttempted > 0 {
		result.ErrorRate = float64(result.Errors) / float64(totalAttempted)
		result.DroppedRate = float64(result.Dropped) / float64(totalAttempted)
	}
	totalWorkTime := result.ProcessingTime + result.WaitTime
	if totalWorkTime > 0 {
		result.Utilization = float64(result.ProcessingTime) / float64(totalWorkTime)
	}

	return result
}

// String returns sorted key:vals string representation of user-defined metrics
func (m *Value) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.userData))
	for k := range m.userData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	metrics := make([]string, 0, len(keys))
	for _, k := range keys {
		metrics = append(metrics, fmt.Sprintf("%s:%d", k, m.userData[k]))
	}

	if len(metrics) > 0 {
		return fmt.Sprintf("[%s]", strings.Join(metrics, ", "))
	}
	return ""
}

// WorkerID returns worker ID from the context
func WorkerID(ctx context.Context) int {
	cid, ok := ctx.Value(widContextKey).(int)
	if !ok {
		return 0
	}
	return cid
}

// WithWorkerID sets worker ID in the context
func WithWorkerID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, widContextKey, id)
}

// Get metrics from context. If not found, creates new instance with same worker count as stored in context.
func Get(ctx context.Context) *Value {
	if v, ok := ctx.Value(metricsContextKey).(*Value); ok {
		return v
	}
	if n, ok := ctx.Value(widContextKey).(int); ok {
		return New(n + 1) // n is max worker id, need size = n+1
	}
	return New(1) // fallback to single worker
}

// Make context with metrics
func Make(ctx context.Context, workers int) context.Context {
	return context.WithValue(ctx, metricsContextKey, New(workers))
}
