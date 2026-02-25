package seoul

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

type benchResult struct {
	value int
	err   error
}

func BenchmarkSeoul(b *testing.B) {
	workloads := []struct {
		name   string
		mixed  bool
		tasks  int
		limit  int
		fast   bool
		failAt int
	}{
		{name: "short/failfast_off", mixed: false, tasks: 256, limit: 32, fast: false, failAt: -1},
		{name: "short/failfast_on", mixed: false, tasks: 256, limit: 32, fast: true, failAt: 0},
		{name: "mixed/failfast_off", mixed: true, tasks: 256, limit: 32, fast: false, failAt: -1},
		{name: "mixed/failfast_on", mixed: true, tasks: 256, limit: 32, fast: true, failAt: 0},
	}

	for _, tc := range workloads {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := runSeoulCase(tc.tasks, tc.limit, tc.mixed, tc.fast, tc.failAt); err != nil {
					b.Fatalf("run failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkErrgroupChannel(b *testing.B) {
	workloads := []struct {
		name   string
		mixed  bool
		tasks  int
		limit  int
		fast   bool
		failAt int
	}{
		{name: "short/failfast_off", mixed: false, tasks: 256, limit: 32, fast: false, failAt: -1},
		{name: "short/failfast_on", mixed: false, tasks: 256, limit: 32, fast: true, failAt: 0},
		{name: "mixed/failfast_off", mixed: true, tasks: 256, limit: 32, fast: false, failAt: -1},
		{name: "mixed/failfast_on", mixed: true, tasks: 256, limit: 32, fast: true, failAt: 0},
	}

	for _, tc := range workloads {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := runErrgroupChannelCase(tc.tasks, tc.limit, tc.mixed, tc.fast, tc.failAt); err != nil {
					b.Fatalf("run failed: %v", err)
				}
			}
		})
	}
}

func runSeoulCase(tasks, limit int, mixed, failFast bool, failAt int) error {
	g := New[int](
		context.Background(),
		WithMaxConcurrency(limit),
		WithFailFast(failFast),
	)

	for i := 0; i < tasks; i++ {
		idx := i
		err := g.Go(func(ctx context.Context) (int, error) {
			return runBenchTask(ctx, idx, mixed, failAt)
		})
		if err != nil {
			return fmt.Errorf("go submit failed: %w", err)
		}
	}
	g.Close()

	for {
		_, ok, err := g.Next(context.Background())
		if err != nil {
			return fmt.Errorf("next failed: %w", err)
		}
		if !ok {
			break
		}
	}

	waitErr := g.Wait()
	if failFast {
		if waitErr == nil {
			return errors.New("expected wait error in fail-fast mode")
		}
		return nil
	}
	if waitErr != nil {
		return fmt.Errorf("unexpected wait error: %w", waitErr)
	}
	return nil
}

func runErrgroupChannelCase(tasks, limit int, mixed, failFast bool, failAt int) error {
	baseCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	eg, runCtx := errgroup.WithContext(baseCtx)
	if limit > 0 {
		eg.SetLimit(limit)
	}

	results := make(chan benchResult, tasks)
	waitDone := make(chan error, 1)

	for i := 0; i < tasks; i++ {
		idx := i
		eg.Go(func() error {
			value, err := runBenchTask(runCtx, idx, mixed, failAt)
			results <- benchResult{value: value, err: err}
			if failFast && err != nil {
				cancel(err)
				return err
			}
			return nil
		})
	}

	go func() {
		waitDone <- eg.Wait()
		close(results)
	}()

	for range results {
	}

	waitErr := <-waitDone
	if failFast {
		if waitErr == nil {
			return errors.New("expected wait error in fail-fast mode")
		}
		return nil
	}
	if waitErr != nil {
		return fmt.Errorf("unexpected wait error: %w", waitErr)
	}
	return nil
}

func runBenchTask(ctx context.Context, idx int, mixed bool, failAt int) (int, error) {
	if failAt >= 0 && idx == failAt {
		return 0, errors.New("boom")
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	if mixed && idx%8 == 0 {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Microsecond):
		}
	}

	return idx, nil
}
