package seoul

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
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

	mustGo(t, g, func(context.Context) (int, error) {
		<-first
		return 1, nil
	})
	mustGo(t, g, func(context.Context) (int, error) {
		<-second
		return 2, nil
	})
	mustGo(t, g, func(context.Context) (int, error) {
		<-third
		return 3, nil
	})
	g.Close()

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

	_, ok, err := g.Next(context.Background())
	if err != nil {
		t.Fatalf("expected err=nil, got %v", err)
	}
	if ok {
		t.Fatal("expected closed+drained group")
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

	mustGo(t, g, func(context.Context) (int, error) {
		close(ready)
		return 0, errBoom
	})
	mustGo(t, g, func(ctx context.Context) (int, error) {
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

func TestGroupFailFastDisabledKeepsRunningTasks(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background(), WithFailFast(false))
	errBoom := errors.New("boom")
	started := make(chan struct{})
	release := make(chan struct{})

	mustGo(t, g, func(context.Context) (int, error) {
		return 0, errBoom
	})
	mustGo(t, g, func(ctx context.Context) (int, error) {
		close(started)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-release:
			return 42, nil
		}
	})

	<-started
	close(release)
	g.Close()

	r1 := mustNext(t, g)
	r2 := mustNext(t, g)
	results := []Result[int]{r1, r2}

	if !containsError([]error{r1.Err, r2.Err}, errBoom) {
		t.Fatalf("expected boom in results, got %+v", results)
	}
	if !containsResult(results, 42, nil) {
		t.Fatalf("expected successful result value=42, got %+v", results)
	}

	_, ok, err := g.Next(context.Background())
	if err != nil {
		t.Fatalf("expected err=nil, got %v", err)
	}
	if ok {
		t.Fatal("expected closed+drained group")
	}

	if err := g.Wait(); !errors.Is(err, errBoom) {
		t.Fatalf("expected wait error=boom, got %v", err)
	}
}

func TestGroupPanicToError(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background(), WithPanicToError(true))

	mustGo(t, g, func(context.Context) (int, error) {
		panic("kaboom")
	})
	mustGo(t, g, func(context.Context) (int, error) {
		return 7, nil
	})
	g.Close()

	r1 := mustNext(t, g)
	r2 := mustNext(t, g)
	results := []Result[int]{r1, r2}

	panicSeen := false
	successSeen := false
	for _, res := range results {
		if res.Err != nil && strings.Contains(res.Err.Error(), "panic recovered: kaboom") {
			panicSeen = true
		}
		if res.Err == nil && res.Value == 7 {
			successSeen = true
		}
	}
	if !panicSeen {
		t.Fatalf("expected panic-converted error in results, got %+v", results)
	}
	if !successSeen {
		t.Fatalf("expected successful result after panic, got %+v", results)
	}

	_, ok, err := g.Next(context.Background())
	if err != nil {
		t.Fatalf("expected err=nil after close+drain, got %v", err)
	}
	if ok {
		t.Fatal("expected closed+drained group")
	}

	if err := g.Wait(); err == nil || !strings.Contains(err.Error(), "panic recovered: kaboom") {
		t.Fatalf("expected panic error from wait, got %v", err)
	}
}

func TestGroupPanicToErrorDisabledRepanics(t *testing.T) {
	t.Parallel()

	if os.Getenv("SEOUL_PANIC_REPANIC_HELPER") == "1" {
		g := New[int](context.Background(), WithPanicToError(false))
		_ = g.Go(func(context.Context) (int, error) {
			panic("kaboom-repanic")
		})
		time.Sleep(100 * time.Millisecond)
		t.Fatal("expected process panic before test returns")
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestGroupPanicToErrorDisabledRepanics")
	cmd.Env = append(os.Environ(), "SEOUL_PANIC_REPANIC_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected helper process to fail from panic, output=%s", string(output))
	}
	if !bytes.Contains(output, []byte("kaboom-repanic")) {
		t.Fatalf("expected panic payload in output, got %s", string(output))
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
		mustGo(t, g, func(context.Context) (int, error) {
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
	g.Close()

	count := 0
	for {
		_, ok, err := g.Next(withTimeout(t, 2*time.Second))
		if err != nil {
			t.Fatalf("unexpected next error: %v", err)
		}
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

func TestGroupGoReturnsErrAfterClose(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())
	g.Close()

	err := g.Go(func(context.Context) (int, error) {
		return 1, nil
	})
	if !errors.Is(err, ErrGroupClosed) {
		t.Fatalf("expected ErrGroupClosed, got %v", err)
	}
}

func TestGroupGoReturnsErrForNilTask(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())
	err := g.Go(nil)
	if !errors.Is(err, ErrNilTask) {
		t.Fatalf("expected ErrNilTask, got %v", err)
	}
}

func TestNextReturnsContextErrorWhenWaiting(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, ok, err := g.Next(ctx)
	if ok {
		t.Fatal("expected ok=false on context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestNextReturnsFalseOnlyAfterCloseAndDrain(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, ok, err := g.Next(ctx)
	if ok {
		t.Fatal("expected ok=false while waiting")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded before close, got %v", err)
	}

	g.Close()
	_, ok, err = g.Next(context.Background())
	if err != nil {
		t.Fatalf("expected err=nil after close+drain, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false after close+drain")
	}
}

func TestCloseDrainsAllPendingNextWaiters(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background())
	const waiters = 8

	type nextOutcome struct {
		ok  bool
		err error
	}

	outcomes := make(chan nextOutcome, waiters)
	var wg sync.WaitGroup
	wg.Add(waiters)

	for i := 0; i < waiters; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, ok, err := g.Next(ctx)
			outcomes <- nextOutcome{ok: ok, err: err}
		}()
	}

	g.Close()
	wg.Wait()
	close(outcomes)

	for out := range outcomes {
		if out.err != nil {
			t.Fatalf("expected waiter err=nil after close+drain, got %v", out.err)
		}
		if out.ok {
			t.Fatal("expected waiter ok=false after close+drain")
		}
	}
}

func TestCanceledWaiterDoesNotConsumeFutureResult(t *testing.T) {
	t.Parallel()

	g := New[int](context.Background(), WithFailFast(false))

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer waitCancel()
	_, ok, err := g.Next(waitCtx)
	if ok {
		t.Fatal("expected ok=false on canceled waiter")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded from canceled waiter, got %v", err)
	}

	mustGo(t, g, func(context.Context) (int, error) {
		return 9, nil
	})
	g.Close()

	res := mustNext(t, g)
	if res.Err != nil || res.Value != 9 {
		t.Fatalf("expected value=9, err=nil, got value=%d err=%v", res.Value, res.Err)
	}

	_, ok, err = g.Next(context.Background())
	if err != nil {
		t.Fatalf("expected err=nil after close+drain, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false after close+drain")
	}
}

func TestConcurrentNextConsumersObserveAllResults(t *testing.T) {
	t.Parallel()

	const (
		taskCount     = 200
		consumerCount = 6
	)

	g := New[int](
		context.Background(),
		WithFailFast(false),
		WithMaxConcurrency(16),
	)

	for i := 0; i < taskCount; i++ {
		i := i
		mustGo(t, g, func(context.Context) (int, error) {
			if i%7 == 0 {
				time.Sleep(2 * time.Millisecond)
			}
			return i, nil
		})
	}
	g.Close()

	results := make(chan Result[int], taskCount)
	errs := make(chan error, consumerCount)
	var wg sync.WaitGroup
	wg.Add(consumerCount)

	for i := 0; i < consumerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				res, ok, err := g.Next(ctx)
				cancel()
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					return
				}
				results <- res
			}
		}()
	}

	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected next error from consumer: %v", err)
		}
	}

	seen := make(map[int]struct{}, taskCount)
	for res := range results {
		if res.Err != nil {
			t.Fatalf("unexpected task error: %v", res.Err)
		}
		if _, dup := seen[res.Value]; dup {
			t.Fatalf("duplicate result value observed: %d", res.Value)
		}
		seen[res.Value] = struct{}{}
	}
	if len(seen) != taskCount {
		t.Fatalf("expected %d unique results, got %d", taskCount, len(seen))
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("expected wait error=nil, got %v", err)
	}
}

func mustNext[T any](t *testing.T, g *Group[T]) Result[T] {
	t.Helper()

	res, ok, err := g.Next(withTimeout(t, 2*time.Second))
	if err != nil {
		t.Fatalf("unexpected next error: %v", err)
	}
	if !ok {
		t.Fatal("expected next result")
	}
	return res
}

func mustGo[T any](t *testing.T, g *Group[T], fn TaskFunc[T]) {
	t.Helper()
	if err := g.Go(fn); err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
}

func containsError(errs []error, target error) bool {
	for _, err := range errs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func containsResult[T comparable](results []Result[T], value T, err error) bool {
	for _, res := range results {
		if res.Value == value && errors.Is(res.Err, err) {
			return true
		}
	}
	return false
}

func withTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
