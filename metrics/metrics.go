// Package metrics provides a way to collect metrics in a thread-safe way.
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

const (
	metricsContextKey contextKey = "metrics"
	widContextKey     contextKey = "worker-id"
)

// metric keys for durations
const (
	DurationWait = "wait"  // time spent waiting for work
	DurationProc = "proc"  // time spent processing work
	DurationInit = "init"  // time spent initializing
	DurationWrap = "wrap"  // time spent wrapping up/finalizing
	DurationFull = "total" // total time since start
)

// metric keys for counters
const (
	CountProcessed = "processed" // number of processed items
	CountErrors    = "errors"    // number of errors
	CountDropped   = "dropped"   // number of dropped items
)

// Value is a struct that holds the metrics for a given context
type Value struct {
	startTime time.Time
	userLock  sync.RWMutex
	userData  map[string]int
	durations map[string]time.Duration
}

// New makes thread-safe map to collect any counts/metrics
func New() *Value {
	return &Value{
		startTime: time.Now(),
		userData:  map[string]int{},
		durations: map[string]time.Duration{},
	}
}

// Add increments value for a given key and returns new value
func (m *Value) Add(key string, delta int) int {
	m.userLock.Lock()
	defer m.userLock.Unlock()
	m.userData[key] += delta
	return m.userData[key]
}

// Inc increments value for given key by one
func (m *Value) Inc(key string) int {
	return m.Add(key, 1)
}

// AddDuration adds duration for a given key
func (m *Value) AddDuration(key string, d time.Duration) {
	m.userLock.Lock()
	defer m.userLock.Unlock()
	m.durations[key] += d
}

// GetDuration returns duration for given key
func (m *Value) GetDuration(key string) time.Duration {
	m.userLock.RLock()
	defer m.userLock.RUnlock()
	return m.durations[key]
}

// Set value for given key
func (m *Value) Set(key string, val int) {
	m.userLock.Lock()
	defer m.userLock.Unlock()
	m.userData[key] = val
}

// Get returns value for given key
func (m *Value) Get(key string) int {
	m.userLock.RLock()
	defer m.userLock.RUnlock()
	return m.userData[key]
}

// StartTimer returns a function that when called will record the duration since StartTimer was called
func (m *Value) StartTimer(key string) func() {
	start := time.Now()
	return func() {
		m.AddDuration(key, time.Since(start))
	}
}

// String returns sorted key:vals string representation of metrics and adds duration
func (m *Value) String() string {
	m.userLock.RLock()
	defer m.userLock.RUnlock()

	// collect all keys for sorting
	keys := make([]string, 0, len(m.userData)+len(m.durations)+1)
	for k := range m.userData {
		keys = append(keys, k)
	}
	for k := range m.durations {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// build metrics string
	var metrics []string
	for _, k := range keys {
		if v, ok := m.userData[k]; ok {
			metrics = append(metrics, fmt.Sprintf("%s:%d", k, v))
		}
		if d, ok := m.durations[k]; ok {
			metrics = append(metrics, fmt.Sprintf("%s:%v", k, d.Round(time.Millisecond)))
		}
	}

	// add total duration
	total := time.Since(m.startTime)
	metrics = append(metrics, fmt.Sprintf("%s:%v", DurationFull, total.Round(time.Millisecond)))

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

// Get metrics from context
func Get(ctx context.Context) *Value {
	res, ok := ctx.Value(metricsContextKey).(*Value)
	if !ok {
		return New()
	}
	return res
}

// Make context with metrics
func Make(ctx context.Context) context.Context {
	return context.WithValue(ctx, metricsContextKey, New())
}

// Aggregate combines multiple metrics values into a single one.
// Adds all counters and durations from the provided values.
func Aggregate(values ...*Value) *Value {
	if len(values) == 0 {
		return New()
	}

	result := New()
	result.startTime = values[0].startTime // use first value's start time

	for _, v := range values {
		v.userLock.RLock()
		// combine counters
		for k, val := range v.userData {
			result.userData[k] += val
		}
		// combine durations
		for k, d := range v.durations {
			result.durations[k] += d
		}
		v.userLock.RUnlock()
	}

	return result
}

// Stats represents all metrics in a single struct
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

// Stats returns all metrics as a single struct
func (m *Value) Stats() Stats {
	m.userLock.RLock()
	defer m.userLock.RUnlock()

	// calculate total time as max of time.Since(startTime) and sum of all durations
	totalTime := time.Since(m.startTime)
	durationsSum := m.durations[DurationProc] + m.durations[DurationWait] +
	  m.durations[DurationInit] + m.durations[DurationWrap]
	if durationsSum > totalTime {
		totalTime = durationsSum
	}

	return Stats{
		Processed:      m.userData[CountProcessed],
		Errors:         m.userData[CountErrors],
		Dropped:        m.userData[CountDropped],
		ProcessingTime: m.durations[DurationProc],
		WaitTime:       m.durations[DurationWait],
		InitTime:       m.durations[DurationInit],
		WrapTime:       m.durations[DurationWrap],
		TotalTime:      totalTime,
	}
}
