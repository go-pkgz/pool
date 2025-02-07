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

// Value is a struct that holds the metrics for a given context
type Value struct {
	startTime time.Time
	userLock  sync.RWMutex
	userData  map[string]int
}

// New makes thread-safe map to collect any counts/metrics
func New() *Value {
	return &Value{startTime: time.Now(), userData: map[string]int{}}
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

// Set value for given key
func (m *Value) Set(key string, val int) {
	m.userLock.Lock()
	defer m.userLock.Unlock()
	m.userData[key] = val
}

// Get returns value for given key
func (m *Value) Get(key string) int {
	m.userLock.RLock()
	defer m.userLock.RUnlock() // nolint gocritic

	return m.userData[key]
}

// String returns sorted key:vals string representation of metrics and adds duration
func (m *Value) String() string {
	duration := time.Since(m.startTime)

	m.userLock.RLock()
	defer m.userLock.RUnlock()

	sortedKeys := func() (res []string) {
		for k := range m.userData {
			res = append(res, k)
		}
		sort.Strings(res)
		return res
	}()

	udata := make([]string, len(sortedKeys))
	for i, k := range sortedKeys {
		udata[i] = fmt.Sprintf("%s:%d", k, m.userData[k])
	}

	um := ""
	if len(udata) > 0 {
		um = fmt.Sprintf("[%s]", strings.Join(udata, ", "))
	}
	return fmt.Sprintf("%v %s", duration, um)
}

// WorkerID returns worker ID from the context.
// Can be used inside of worker code to get worker id.
func WorkerID(ctx context.Context) int {
	cid, ok := ctx.Value(widContextKey).(int)
	if !ok { // for non-parallel won't have any
		cid = 0
	}
	return cid
}

// WithWorkerID sets worker ID in the context.
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
