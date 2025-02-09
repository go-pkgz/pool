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

// Stats represents worker-specific metrics
type Stats struct {
	Processed      int
	Errors         int
	Dropped        int
	ProcessingTime time.Duration
	WaitTime       time.Duration
	InitTime       time.Duration
	WrapTime       time.Duration
	TotalTime      time.Duration
}

// String returns stats info formatted as string
func (s Stats) String() string {
	var metrics []string

	if s.Processed > 0 {
		metrics = append(metrics, fmt.Sprintf("processed:%d", s.Processed))
	}
	if s.Errors > 0 {
		metrics = append(metrics, fmt.Sprintf("errors:%d", s.Errors))
	}
	if s.Dropped > 0 {
		metrics = append(metrics, fmt.Sprintf("dropped:%d", s.Dropped))
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
	for i := range m.workerStats {
		result.Processed += m.workerStats[i].Processed
		result.Errors += m.workerStats[i].Errors
		result.Dropped += m.workerStats[i].Dropped
		result.ProcessingTime += m.workerStats[i].ProcessingTime
		result.WaitTime += m.workerStats[i].WaitTime
		result.InitTime += m.workerStats[i].InitTime
		result.WrapTime += m.workerStats[i].WrapTime
	}
	result.TotalTime = time.Since(m.startTime)
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
