package pool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollector_Basic(t *testing.T) {
	ctx := context.Background()
	c := NewCollector[string](ctx, 5)

	go func() {
		c.Submit("test1")
		c.Submit("test2")
		c.Submit("test3")
		c.Close()
	}()

	var values []string
	var lastErr error
	for v, err := range c.Iter() {
		if err != nil {
			lastErr = err
			break
		}
		values = append(values, v)
	}

	require.NoError(t, lastErr)
	require.Equal(t, []string{"test1", "test2", "test3"}, values)
}

func TestCollector_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	c := NewCollector[int](ctx, 5)

	go func() {
		c.Submit(1)
		time.Sleep(time.Second) // simulate slow producer
		c.Submit(2)
		c.Close()
	}()

	var values []int
	var lastErr error
	for v, err := range c.Iter() {
		if err != nil {
			lastErr = err
			break
		}
		values = append(values, v)
	}

	require.ErrorIs(t, lastErr, context.DeadlineExceeded)
	require.Equal(t, []int{1}, values)
}

func TestCollector_All(t *testing.T) {
	ctx := context.Background()
	c := NewCollector[int](ctx, 5)

	go func() {
		for i := 0; i < 3; i++ {
			c.Submit(i)
		}
		c.Close()
	}()

	values, err := c.All()
	require.NoError(t, err)
	require.Equal(t, []int{0, 1, 2}, values)
}

func TestCollector_All_WithError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	c := NewCollector[int](ctx, 5)

	go func() {
		c.Submit(1)
		time.Sleep(time.Second)
		c.Submit(2)
		c.Close()
	}()

	values, err := c.All()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, []int{1}, values)
}

func TestCollector_Multiple(t *testing.T) {
	ctx := context.Background()
	c1 := NewCollector[string](ctx, 5)
	c2 := NewCollector[string](ctx, 5)

	go func() {
		c1.Submit("c1-1")
		c1.Submit("c1-2")
		c1.Close()
	}()

	go func() {
		c2.Submit("c2-1")
		c2.Submit("c2-2")
		c2.Close()
	}()

	v1, err := c1.All()
	require.NoError(t, err)
	require.Equal(t, []string{"c1-1", "c1-2"}, v1)

	v2, err := c2.All()
	require.NoError(t, err)
	require.Equal(t, []string{"c2-1", "c2-2"}, v2)
}
