package pool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	t.Run("worker and worker maker are mutually exclusive", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })
		workerMaker := func() Worker[string] { return worker }

		// both worker and maker provided should fail
		_, err := New[string](1,
			opts.WithWorker(worker),
			opts.WithWorkerMaker(workerMaker),
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "both worker and worker maker provided")

		// neither worker nor maker provided should fail
		_, err = New[string](1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "worker or worker maker not provided")
	})

	t.Run("options are properly applied", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })
		ctx := context.Background()

		customCtx := context.WithValue(ctx, "test", "value") //nolint
		completeFn := func(ctx context.Context, id int, w Worker[string]) error { return nil }
		chunkFn := func(v string) string { return v }

		p, err := New[string](2,
			opts.WithWorker(worker),
			opts.WithBatchSize(10),
			opts.WithWorkerChanSize(5),
			opts.WithContext(customCtx),
			opts.WithCompleteFn(completeFn),
			opts.WithChunkFn(chunkFn),
			opts.WithContinueOnError(),
		)
		require.NoError(t, err)

		// verify all options were applied correctly
		require.Equal(t, 2, p.poolSize)
		require.Equal(t, 10, p.batchSize)
		require.Equal(t, 5, p.workerChanSize)
		require.Equal(t, customCtx, p.ctx)
		require.NotNil(t, p.worker)
		require.NotNil(t, p.completeFn)
		require.NotNil(t, p.chunkFn)
		require.True(t, p.continueOnError)

		// verify channels were initialized with correct sizes
		require.Len(t, p.workersCh, 2)
		require.Equal(t, 5, cap(p.workersCh[0]))
		require.Equal(t, 5, cap(p.workersCh[1]))
	})

	t.Run("worker maker creates new instances", func(t *testing.T) {
		opts := Options[string]()

		workerMaker := func() Worker[string] {
			return WorkerFunc[string](func(ctx context.Context, v string) error {
				return nil
			})
		}

		p, err := New[string](2,
			opts.WithWorkerMaker(workerMaker),
		)
		require.NoError(t, err)
		require.Nil(t, p.worker)
		require.NotNil(t, p.workerMaker)
	})

	t.Run("default values are set when options not provided", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })

		p, err := New[string](1,
			opts.WithWorker(worker),
		)
		require.NoError(t, err)

		// verify defaults
		require.Equal(t, 1, p.batchSize)
		require.Equal(t, 1, p.workerChanSize)
		require.False(t, p.continueOnError)
		require.Nil(t, p.completeFn)
		require.Nil(t, p.chunkFn)
		require.NotNil(t, p.ctx)
	})
}
