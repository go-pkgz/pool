package metrics

import (
	"context"
	"testing"

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
	assert.Contains(t, m.String(), "[k1:101, k2:1]")
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
