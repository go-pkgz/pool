package pool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	t.Run("options are properly applied", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })
		ctx := context.Background()

		customCtx := context.WithValue(ctx, "test", "value") //nolint
		completeFn := func(ctx context.Context, id int, w Worker[string]) error { return nil }
		chunkFn := func(v string) string { return v }

		p, err := New[string](2, worker,
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

	t.Run("invalid options return errors", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })

		tests := []struct {
			name    string
			option  Option[string]
			wantErr string
		}{
			{
				name:    "batch size zero",
				option:  opts.WithBatchSize(0),
				wantErr: "batch size must be greater than 0",
			},
			{
				name:    "batch size negative",
				option:  opts.WithBatchSize(-1),
				wantErr: "batch size must be greater than 0",
			},
			{
				name:    "worker channel size zero",
				option:  opts.WithWorkerChanSize(0),
				wantErr: "worker channel size must be greater than 0",
			},
			{
				name:    "worker channel size negative",
				option:  opts.WithWorkerChanSize(-1),
				wantErr: "worker channel size must be greater than 0",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := New[string](2, worker, tt.option)
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			})
		}
	})

	t.Run("stateful worker creates new instances", func(t *testing.T) {
		opts := Options[string]()
		workerMaker := func() Worker[string] {
			return WorkerFunc[string](func(ctx context.Context, v string) error {
				return nil
			})
		}

		p, err := NewStateful[string](2, workerMaker,
			opts.WithBatchSize(10),
		)
		require.NoError(t, err)
		require.Nil(t, p.worker)
		require.NotNil(t, p.workerMaker)
	})

	t.Run("default values are set when options not provided", func(t *testing.T) {
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })

		p, err := New[string](1, worker)
		require.NoError(t, err)

		// verify defaults
		require.Equal(t, 1, p.batchSize)
		require.Equal(t, 1, p.workerChanSize)
		require.False(t, p.continueOnError)
		require.Nil(t, p.completeFn)
		require.Nil(t, p.chunkFn)
		require.NotNil(t, p.ctx)
	})

	t.Run("multiple option errors are reported", func(t *testing.T) {
		opts := Options[string]()
		worker := WorkerFunc[string](func(ctx context.Context, v string) error { return nil })

		_, err := New[string](2, worker,
			opts.WithBatchSize(0),
			opts.WithWorkerChanSize(-1),
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "batch size must be greater than 0")
	})
}
