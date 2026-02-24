package seoul

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupNextReturnsCompletionOrder(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background(), WithFailFast(false))

	first := make(chan struct{})
	second := make(chan struct{})
	third := make(chan struct{})

	g.Go(func(context.Context) (int, error) {
		<-first
		return 1, nil
	})
	g.Go(func(context.Context) (int, error) {
		<-second
		return 2, nil
	})
	g.Go(func(context.Context) (int, error) {
		<-third
		return 3, nil
	})

	close(second)
	got := mustNext(t, g)
	if got.Err != nil || got.Value != 2 {
		t.Fatalf("expected value=2, err=nil, got value=%d err=%v", got.Value, got.Err)
	}

	close(third)
	got = mustNext(t, g)
	if got.Err != nil || got.Value != 3 {
		t.Fatalf("expected value=3, err=nil, got value=%d err=%v", got.Value, got.Err)
	}

	close(first)
	got = mustNext(t, g)
	if got.Err != nil || got.Value != 1 {
		t.Fatalf("expected value=1, err=nil, got value=%d err=%v", got.Value, got.Err)
	}

	if _, ok := g.Next(); ok {
		t.Fatal("expected empty group")
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("expected wait error=nil, got %v", err)
	}
}

func TestGroupFailFastCancelsRemainingTasks(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())
	errBoom := errors.New("boom")
	ready := make(chan struct{})

	g.Go(func(context.Context) (int, error) {
		close(ready)
		return 0, errBoom
	})
	g.Go(func(ctx context.Context) (int, error) {
		<-ready
		<-ctx.Done()
		return 0, ctx.Err()
	})

	r1 := mustNext(t, g)
	r2 := mustNext(t, g)
	errs := []error{r1.Err, r2.Err}

	if !containsError(errs, errBoom) {
		t.Fatalf("expected boom in results, got %v", errs)
	}
	if !containsError(errs, context.Canceled) {
		t.Fatalf("expected context.Canceled in results, got %v", errs)
	}

	if err := g.Wait(); !errors.Is(err, errBoom) {
		t.Fatalf("expected wait error=boom, got %v", err)
	}
}

func TestGroupPanicToError(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background(), WithPanicToError(true))

	g.Go(func(context.Context) (int, error) {
		panic("kaboom")
	})

	res := mustNext(t, g)
	if res.Err == nil {
		t.Fatal("expected panic to be converted to error")
	}
	if !strings.Contains(res.Err.Error(), "panic recovered: kaboom") {
		t.Fatalf("unexpected panic error: %v", res.Err)
	}

	if err := g.Wait(); err == nil || !strings.Contains(err.Error(), "panic recovered: kaboom") {
		t.Fatalf("expected panic error from wait, got %v", err)
	}
}

func TestGroupMaxConcurrency(t *testing.T) {
	t.Parallel()

	const limit = int32(2)
	const total = 10

	g := New[int](context.Background(), WithFailFast(false), WithMaxConcurrency(int(limit)))

	var running int32
	var maxRunning int32

	for i := 0; i < total; i++ {
		g.Go(func(context.Context) (int, error) {
			curr := atomic.AddInt32(&running, 1)
			for {
				prev := atomic.LoadInt32(&maxRunning)
				if curr <= prev || atomic.CompareAndSwapInt32(&maxRunning, prev, curr) {
					break
				}
			}

			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return 1, nil
		})
	}

	count := 0
	for {
		_, ok := g.Next()
		if !ok {
			break
		}
		count++
	}
	if count != total {
		t.Fatalf("expected %d results, got %d", total, count)
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("expected wait error=nil, got %v", err)
	}
	if got := atomic.LoadInt32(&maxRunning); got > limit {
		t.Fatalf("max concurrency exceeded: got %d, limit %d", got, limit)
	}
}

func TestWithMaxConcurrencyPanicsForNegativeInput(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for negative max concurrency")
		}
	}()

	_ = WithMaxConcurrency(-1)
}

func mustNext[T any](t *testing.T, g *Group[T]) Result[T] {
	t.Helper()

	res, ok := g.Next()
	if !ok {
		t.Fatal("expected next result")
	}
	return res
}

func containsError(errs []error, target error) bool {
	for _, err := range errs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
