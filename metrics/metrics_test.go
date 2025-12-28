package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_UserDefined(t *testing.T) {
	m := New(1) // single worker is enough for user stats testing

	t.Run("basic operations", func(t *testing.T) {
		m.Add("k1", 100)
		m.Inc("k1")
		m.Inc("k2")

		assert.Equal(t, 101, m.Get("k1"))
		assert.Equal(t, 1, m.Get("k2"))
		assert.Equal(t, 0, m.Get("k3"))

		str := m.String()
		assert.Contains(t, str, "k1:101")
		assert.Contains(t, str, "k2:1")
	})

	t.Run("string formatting", func(t *testing.T) {
		m := New(10)
		assert.Empty(t, m.String(), "empty metrics should return empty string")

		m.Inc("test")
		assert.Equal(t, "[test:1]", m.String())

		m.Add("another", 5)
		str := m.String()
		assert.Contains(t, str, "test:1")
		assert.Contains(t, str, "another:5")
	})
}

func TestMetrics_WorkerStats(t *testing.T) {
	t.Run("worker timers", func(t *testing.T) {
		m := New(2) // create metrics for 2 workers

		// worker 1 operations
		end := m.StartTimer(0, TimerProc)
		time.Sleep(20 * time.Millisecond)
		end()

		end = m.StartTimer(0, TimerWait)
		time.Sleep(20 * time.Millisecond)
		end()

		// worker 2 operations
		end = m.StartTimer(1, TimerProc)
		time.Sleep(20 * time.Millisecond)
		end()

		stats := m.GetStats()
		assert.GreaterOrEqual(t, stats.ProcessingTime.Milliseconds(), int64(20),
			"processing time should be at least 20ms")
		assert.GreaterOrEqual(t, stats.WaitTime.Milliseconds(), int64(20),
			"wait time should be at least 20ms")
		assert.Greater(t, stats.TotalTime, stats.WaitTime,
			"total time should be greater than wait time")
		assert.Greater(t, stats.TotalTime, stats.ProcessingTime,
			"total time should be greater than processing time")
	})

	t.Run("worker counters", func(t *testing.T) {
		// worker 1 increments
		m := New(2)
		m.IncProcessed(0)
		m.IncProcessed(0)
		m.IncErrors(0)

		// worker 2 increments
		m.IncProcessed(1)
		m.IncDropped(1)

		stats := m.GetStats()
		assert.Equal(t, 3, stats.Processed)
		assert.Equal(t, 1, stats.Errors)
		assert.Equal(t, 1, stats.Dropped)
	})

	t.Run("stats string", func(t *testing.T) {
		m := New(1)
		m.IncProcessed(0)
		m.IncErrors(0)
		end := m.StartTimer(0, TimerProc)
		time.Sleep(10 * time.Millisecond)
		end()

		stats := m.GetStats()
		str := stats.String()
		assert.Contains(t, str, "processed:1")
		assert.Contains(t, str, "errors:1")
		assert.Contains(t, str, "proc:")
		assert.Contains(t, str, "total:")
	})
}

func TestMetrics_Context(t *testing.T) {
	t.Run("worker id", func(t *testing.T) {
		ctx := WithWorkerID(context.Background(), 123)
		assert.Equal(t, 123, WorkerID(ctx))

		ctx = context.Background()
		assert.Equal(t, 0, WorkerID(ctx))

		ctx = context.WithValue(context.Background(), widContextKey, "not an int")
		assert.Equal(t, 0, WorkerID(ctx))
	})

	t.Run("metrics from context", func(t *testing.T) {
		ctx := Make(context.Background(), 2)
		m := Get(ctx)
		require.NotNil(t, m)

		// verify metrics working
		m.Inc("test")
		assert.Equal(t, 1, m.Get("test"))

		// verify worker stats
		m.IncProcessed(0)
		stats := m.GetStats()
		assert.Equal(t, 1, stats.Processed)
	})

	t.Run("metrics isolation", func(t *testing.T) {
		ctx1 := Make(context.Background(), 1)
		ctx2 := Make(context.Background(), 1)

		m1 := Get(ctx1)
		m2 := Get(ctx2)

		m1.Inc("test")
		assert.Equal(t, 1, m1.Get("test"))
		assert.Equal(t, 0, m2.Get("test"))
	})

	t.Run("metrics creation from worker id", func(t *testing.T) {
		ctx := WithWorkerID(context.Background(), 5)
		m := Get(ctx)
		require.NotNil(t, m)

		// should be able to use worker id 5
		m.IncProcessed(5)
		stats := m.GetStats()
		assert.Equal(t, 1, stats.Processed)
	})
}

func TestMetrics_Concurrent(t *testing.T) {
	t.Run("concurrent user stats access", func(t *testing.T) {
		m := New(1)
		const goroutines = 10
		const iterations = 1000

		var wg sync.WaitGroup
		wg.Add(goroutines)

		for range goroutines {
			go func() {
				defer wg.Done()
				for range iterations {
					m.Inc("counter")
					val := m.Get("counter")
					assert.Positive(t, val)
					m.Add("sum", 2)
				}
			}()
		}
		wg.Wait()

		assert.Equal(t, goroutines*iterations, m.Get("counter"))
		assert.Equal(t, goroutines*iterations*2, m.Get("sum"))
	})

	t.Run("per worker stats", func(t *testing.T) {
		const workers = 4
		m := New(workers)
		var wg sync.WaitGroup
		wg.Add(workers)

		// each worker operates on its own stats
		for wid := range workers {
			go func(id int) {
				defer wg.Done()
				const iterations = 1000

				for range iterations {
					m.IncProcessed(id)
					end := m.StartTimer(id, TimerProc)
					time.Sleep(time.Microsecond)
					end()
				}
			}(wid)
		}
		wg.Wait()

		stats := m.GetStats()
		assert.Equal(t, workers*1000, stats.Processed)
		assert.Greater(t, stats.ProcessingTime, time.Duration(0))

		// verify each worker's stats are accurate
		for wid := range workers {
			assert.Equal(t, 1000, m.workerStats[wid].Processed)
			assert.Greater(t, m.workerStats[wid].ProcessingTime, time.Duration(0))
		}
	})
}

func TestMetrics_AllTimerTypes(t *testing.T) {
	m := New(1)

	// record each timer type
	end := m.StartTimer(0, TimerProc)
	time.Sleep(time.Millisecond)
	end()

	end = m.StartTimer(0, TimerWait)
	time.Sleep(time.Millisecond)
	end()

	end = m.StartTimer(0, TimerInit)
	time.Sleep(time.Millisecond)
	end()

	end = m.StartTimer(0, TimerWrap)
	time.Sleep(time.Millisecond)
	end()

	// verify each timer recorded something
	stats := m.workerStats[0]
	assert.Greater(t, stats.ProcessingTime, time.Duration(0), "ProcessingTime should be recorded")
	assert.Greater(t, stats.WaitTime, time.Duration(0), "WaitTime should be recorded")
	assert.Greater(t, stats.InitTime, time.Duration(0), "InitTime should be recorded")
	assert.Greater(t, stats.WrapTime, time.Duration(0), "WrapTime should be recorded")

	// test unknown timer type
	end = m.StartTimer(0, TimerType(99))
	time.Sleep(time.Millisecond)
	end()
	// stats should remain unchanged
	newStats := m.workerStats[0]
	assert.Equal(t, stats, newStats, "unknown timer type should not affect stats")
}

func TestStats_String(t *testing.T) {
	tests := []struct {
		name     string
		stats    Stats
		expected string
	}{
		{
			name:     "empty stats",
			stats:    Stats{},
			expected: "",
		},
		{
			name: "only counters",
			stats: Stats{
				Processed: 10,
				Errors:    2,
				Dropped:   3,
			},
			expected: "[processed:10, errors:2, dropped:3]",
		},
		{
			name: "only timers",
			stats: Stats{
				ProcessingTime: time.Second,
				WaitTime:       2 * time.Second,
				InitTime:       3 * time.Second,
				WrapTime:       4 * time.Second,
				TotalTime:      10 * time.Second,
			},
			expected: "[proc:1s, wait:2s, init:3s, wrap:4s, total:10s]",
		},
		{
			name: "all fields",
			stats: Stats{
				Processed:      10,
				Errors:         2,
				Dropped:        3,
				ProcessingTime: time.Second,
				WaitTime:       2 * time.Second,
				InitTime:       3 * time.Second,
				WrapTime:       4 * time.Second,
				TotalTime:      10 * time.Second,
			},
			expected: "[processed:10, errors:2, dropped:3, proc:1s, wait:2s, init:3s, wrap:4s, total:10s]",
		},
		{
			name: "some fields zero",
			stats: Stats{
				Processed:      10,
				ProcessingTime: time.Second,
				TotalTime:      10 * time.Second,
			},
			expected: "[processed:10, proc:1s, total:10s]",
		},
		{
			name: "with derived stats",
			stats: Stats{
				Processed:      100,
				Errors:         10,
				ProcessingTime: time.Second,
				WaitTime:       time.Second,
				TotalTime:      2 * time.Second,
				RatePerSec:     50.0,
				AvgLatency:     10 * time.Millisecond,
				ErrorRate:      0.1,
				Utilization:    0.5,
			},
			expected: "[processed:100, rate:50.0/s, avg_latency:10ms, errors:10 (10.0%), proc:1s, wait:1s, total:2s, utilization:50.0%]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.stats.String())
		})
	}
}

func TestMetrics_AddWaitTime(t *testing.T) {
	t.Run("basic wait time tracking", func(t *testing.T) {
		m := New(2) // two workers

		// add some wait time to worker 0
		m.AddWaitTime(0, 100*time.Millisecond)
		m.AddWaitTime(0, 50*time.Millisecond)

		// add different wait time to worker 1
		m.AddWaitTime(1, 75*time.Millisecond)

		stats := m.GetStats()
		assert.Equal(t, 225*time.Millisecond, stats.WaitTime,
			"total wait time should be sum of all workers' wait times")
	})

	t.Run("accumulation with existing timers", func(t *testing.T) {
		m := New(1)

		// start a regular wait timer
		end := m.StartTimer(0, TimerWait)
		time.Sleep(10 * time.Millisecond)
		end()

		// add explicit wait time
		m.AddWaitTime(0, 20*time.Millisecond)

		stats := m.GetStats()
		assert.Greater(t, stats.WaitTime, 30*time.Millisecond,
			"wait time should include both timer and added wait time")
	})

	t.Run("multiple workers tracking", func(t *testing.T) {
		m := New(3)

		// simulate different wait patterns for each worker
		m.AddWaitTime(0, 10*time.Millisecond)
		m.AddWaitTime(1, 20*time.Millisecond)
		m.AddWaitTime(2, 30*time.Millisecond)

		// add more wait time to first worker
		m.AddWaitTime(0, 15*time.Millisecond)

		stats := m.GetStats()
		assert.Equal(t, 75*time.Millisecond, stats.WaitTime,
			"total wait time should be sum across all workers")
		assert.Equal(t, 25*time.Millisecond, m.workerStats[0].WaitTime,
			"individual worker should track its own wait time")
	})
}

func TestStats_DerivedValues(t *testing.T) {
	t.Run("derived stats calculation", func(t *testing.T) {
		m := New(1)
		w := m.workerStats[0]
		w.Processed = 100
		w.Errors = 10
		w.Dropped = 5
		w.ProcessingTime = 2 * time.Second
		w.WaitTime = 1 * time.Second
		m.workerStats[0] = w
		m.startTime = time.Now().Add(-4 * time.Second) // simulate 4 seconds total time

		stats := m.GetStats()

		assert.InDelta(t, 25.0, stats.RatePerSec, 0.1, "should calculate rate per second")
		assert.Equal(t, 20*time.Millisecond, stats.AvgLatency, "should calculate average latency")
		assert.InDelta(t, 0.087, stats.ErrorRate, 0.01, "should calculate error rate")
		assert.InDelta(t, 0.043, stats.DroppedRate, 0.01, "should calculate dropped rate")
		assert.InDelta(t, 0.67, stats.Utilization, 0.01, "should calculate utilization")
	})

	t.Run("string format with derived stats", func(t *testing.T) {
		stats := Stats{
			Processed:      100,
			Errors:         10,
			Dropped:        5,
			ProcessingTime: 2 * time.Second,
			WaitTime:       1 * time.Second,
			TotalTime:      4 * time.Second,
			RatePerSec:     25.0,
			AvgLatency:     20 * time.Millisecond,
			ErrorRate:      0.087,
			DroppedRate:    0.043,
			Utilization:    0.67,
		}

		str := stats.String()
		t.Log("Stats string:", str)
		assert.Contains(t, str, "rate:25.0/s")
		assert.Contains(t, str, "avg_latency:20ms")
		assert.Contains(t, str, "errors:10 (8.7%)")
		assert.Contains(t, str, "dropped:5 (4.3%)")
		assert.Contains(t, str, "utilization:67.0%")
	})

	t.Run("handles zero values", func(t *testing.T) {
		m := New(1)
		stats := m.GetStats()

		assert.Zero(t, stats.RatePerSec)
		assert.Zero(t, stats.AvgLatency)
		assert.Zero(t, stats.ErrorRate)
		assert.Zero(t, stats.DroppedRate)
		assert.Zero(t, stats.Utilization)
	})
}

func TestMetrics_ParallelProcessing(t *testing.T) {
	m := New(2) // two workers

	// simulate two workers processing in parallel
	// worker 1: processes for 100ms
	m.workerStats[0].ProcessingTime = 100 * time.Millisecond
	m.workerStats[0].Processed = 50

	// worker 2: processes for 150ms
	m.workerStats[1].ProcessingTime = 150 * time.Millisecond
	m.workerStats[1].Processed = 75

	// set start time to simulate 200ms total elapsed time
	m.startTime = time.Now().Add(-200 * time.Millisecond)

	stats := m.GetStats()

	// processing time should be max of workers, not sum
	assert.Equal(t, 150*time.Millisecond, stats.ProcessingTime,
		"processing time should be max across workers")

	// total time should be elapsed wall time
	assert.InDelta(t, 200, stats.TotalTime.Milliseconds(), 50,
		"total time should be actual elapsed time")

	// rate should be total processed divided by total time
	expectedRate := float64(stats.Processed) / stats.TotalTime.Seconds()
	assert.InDelta(t, expectedRate, stats.RatePerSec, 1,
		"rate should be based on total processed items and elapsed time")

	// average latency should use max processing time
	expectedLatency := stats.ProcessingTime / time.Duration(stats.Processed)
	assert.Equal(t, expectedLatency, stats.AvgLatency,
		"average latency should be based on max processing time")
}
