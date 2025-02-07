package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetrics(t *testing.T) {
	m := New()

	m.Add("k1", 100)
	m.Inc("k1")

	m.Inc("k2")

	t.Log(m)

	assert.Equal(t, 101, m.Get("k1"))
	assert.Equal(t, 1, m.Get("k2"))
	assert.Equal(t, 0, m.Get("k3"))

	str := m.String()
	assert.Contains(t, str, "k1:101")
	assert.Contains(t, str, "k2:1")
	assert.Contains(t, str, "total:") // just verify total is present
}

func TestWorkerID(t *testing.T) {
	ctx := context.Background()
	ctx = WithWorkerID(ctx, 123)
	assert.Equal(t, 123, WorkerID(ctx))
}

func TestGet(t *testing.T) {
	ctx := Make(context.Background())
	val := Get(ctx)
	val.Set("k1", 100)
	val.Inc("k1")

	vv := Get(ctx)
	assert.Equal(t, 101, vv.Get("k1"))
}

func TestMetricsEnhanced(t *testing.T) {
	m := New()

	// simulate initialization
	initEnd := m.StartTimer(DurationInit)
	time.Sleep(10 * time.Millisecond)
	initEnd()

	// simulate wait and processing
	waitEnd := m.StartTimer(DurationWait)
	time.Sleep(10 * time.Millisecond)
	waitEnd()

	procEnd := m.StartTimer(DurationProc)
	time.Sleep(20 * time.Millisecond)
	procEnd()

	m.Inc(CountProcessed)
	m.Inc(CountProcessed)
	m.Inc(CountErrors)

	assert.Greater(t, m.GetDuration(DurationInit), time.Duration(0))
	assert.Greater(t, m.GetDuration(DurationWait), time.Duration(0))
	assert.Greater(t, m.GetDuration(DurationProc), time.Duration(0))
	assert.Equal(t, 2, m.Get(CountProcessed))
	assert.Equal(t, 1, m.Get(CountErrors))

	// check string representation includes all metrics
	str := m.String()
	t.Log("Metrics:", str)
	assert.Contains(t, str, DurationInit+":")
	assert.Contains(t, str, DurationWait+":")
	assert.Contains(t, str, DurationProc+":")
	assert.Contains(t, str, CountProcessed+":2")
	assert.Contains(t, str, CountErrors+":1")
}

func TestMetrics_Aggregate(t *testing.T) {
	m1 := New()
	m1.Inc(CountProcessed)
	m1.Inc(CountErrors)
	m1.AddDuration(DurationWait, time.Second)

	m2 := New()
	m2.Inc(CountProcessed)
	m2.Inc(CountProcessed)
	m2.AddDuration(DurationWait, 2*time.Second)
	m2.AddDuration(DurationProc, time.Second)

	combined := Aggregate(m1, m2)
	assert.Equal(t, 3, combined.Get(CountProcessed))
	assert.Equal(t, 1, combined.Get(CountErrors))
	assert.Equal(t, 3*time.Second, combined.GetDuration(DurationWait))
	assert.Equal(t, time.Second, combined.GetDuration(DurationProc))
}
