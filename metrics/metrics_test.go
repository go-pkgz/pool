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

func TestMetricsStats_Timing(t *testing.T) {
	m := New()

	// simulate some processing that takes time
	procEnd := m.StartTimer(DurationProc)
	time.Sleep(50 * time.Millisecond)
	procEnd()

	// get stats right after processing
	stats := m.Stats()

	t.Logf("Total time: %v", stats.TotalTime)
	t.Logf("Processing time: %v", stats.ProcessingTime)

	// total time should be greater than or equal to processing time
	assert.GreaterOrEqual(t, stats.TotalTime, stats.ProcessingTime,
		"total time (%v) should be >= processing time (%v)",
		stats.TotalTime, stats.ProcessingTime)
}

func TestMetrics_ContextValues(t *testing.T) {
	t.Run("worker id from context", func(t *testing.T) {
		t.Run("valid worker id", func(t *testing.T) {
			ctx := WithWorkerID(context.Background(), 123)
			assert.Equal(t, 123, WorkerID(ctx))
		})

		t.Run("missing worker id", func(t *testing.T) {
			ctx := context.Background()
			assert.Equal(t, 0, WorkerID(ctx))
		})

		t.Run("invalid worker id type", func(t *testing.T) {
			ctx := context.WithValue(context.Background(), widContextKey, "not an int")
			assert.Equal(t, 0, WorkerID(ctx))
		})
	})

	t.Run("metrics from context", func(t *testing.T) {
		t.Run("valid metrics", func(t *testing.T) {
			ctx := Make(context.Background())
			m := Get(ctx)
			assert.NotNil(t, m)

			// verify it's a working metrics instance
			m.Inc("test")
			assert.Equal(t, 1, m.Get("test"))
		})

		t.Run("missing metrics", func(t *testing.T) {
			ctx := context.Background()
			m := Get(ctx)
			assert.NotNil(t, m, "should return new metrics instance")

			// verify it's a working metrics instance
			m.Inc("test")
			assert.Equal(t, 1, m.Get("test"))
		})

		t.Run("invalid metrics type", func(t *testing.T) {
			ctx := context.WithValue(context.Background(), metricsContextKey, "not metrics")
			m := Get(ctx)
			assert.NotNil(t, m, "should return new metrics instance")

			// verify it's a working metrics instance
			m.Inc("test")
			assert.Equal(t, 1, m.Get("test"))
		})

		t.Run("metrics values isolated", func(t *testing.T) {
			ctx1 := Make(context.Background())
			ctx2 := Make(context.Background())

			m1 := Get(ctx1)
			m2 := Get(ctx2)

			m1.Inc("test")
			assert.Equal(t, 1, m1.Get("test"))
			assert.Equal(t, 0, m2.Get("test"), "metrics should be isolated")
		})
	})

	t.Run("combined worker id and metrics", func(t *testing.T) {
		ctx := Make(context.Background())
		ctx = WithWorkerID(ctx, 42)

		assert.Equal(t, 42, WorkerID(ctx))
		m := Get(ctx)
		assert.NotNil(t, m)

		// verify metrics working
		m.Inc("test")
		assert.Equal(t, 1, m.Get("test"))
	})
}

func TestMetrics_Stats(t *testing.T) {
	t.Run("all durations", func(t *testing.T) {
		m := New()
		m.AddDuration(DurationProc, time.Second)
		m.AddDuration(DurationWait, 2*time.Second)
		m.AddDuration(DurationInit, 3*time.Second)
		m.AddDuration(DurationWrap, 4*time.Second)

		stats := m.Stats()
		assert.Equal(t, time.Second, stats.ProcessingTime)
		assert.Equal(t, 2*time.Second, stats.WaitTime)
		assert.Equal(t, 3*time.Second, stats.InitTime)
		assert.Equal(t, 4*time.Second, stats.WrapTime)

		// total time should be max of time.Since(startTime) and sum of all durations
		expectedTotal := 10 * time.Second // sum of all durations
		assert.GreaterOrEqual(t, stats.TotalTime, expectedTotal)
	})

	t.Run("all counters", func(t *testing.T) {
		m := New()
		m.Inc(CountProcessed)
		m.Inc(CountProcessed)
		m.Inc(CountErrors)
		m.Inc(CountDropped)
		m.Inc(CountDropped)
		m.Inc(CountDropped)

		stats := m.Stats()
		assert.Equal(t, 2, stats.Processed)
		assert.Equal(t, 1, stats.Errors)
		assert.Equal(t, 3, stats.Dropped)
	})

	t.Run("total time calculation", func(t *testing.T) {
		m := New()

		// make sure some time passes
		time.Sleep(time.Millisecond * 10)

		// add durations less than elapsed time
		m.AddDuration(DurationProc, time.Millisecond)
		m.AddDuration(DurationWait, time.Millisecond)

		stats := m.Stats()
		// total time should be time.Since(startTime) as it's greater
		assert.Greater(t, stats.TotalTime, 2*time.Millisecond)

		// now add duration greater than elapsed
		m.AddDuration(DurationProc, time.Second)
		stats = m.Stats()
		// total time should be sum of durations as it's greater
		assert.Greater(t, stats.TotalTime, time.Second)
	})
}
