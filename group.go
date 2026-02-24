package seoul

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// TaskFunc is a unit of work managed by Group.
type TaskFunc[T any] func(context.Context) (T, error)

// Result is returned by Next in task-completion order.
type Result[T any] struct {
	Value T
	Err   error
}

var (
	// ErrGroupClosed is returned by Go when submission happens after Close.
	ErrGroupClosed = errors.New("seoul: group is closed")

	// ErrNilTask is returned by Go when the task callback is nil.
	ErrNilTask = errors.New("seoul: nil task")
)

// Group runs tasks and streams completed results with Next.
type Group[T any] struct {
	ctx     context.Context
	baseCtx context.Context
	cancel  context.CancelCauseFunc
	eg      *errgroup.Group
	cfg     config

	mu       sync.Mutex
	closed   bool
	pending  int
	queue    []Result[T]
	firstErr error
	notify   chan struct{}

	cancelOnce sync.Once
}

// New creates a new Group.
func New[T any](ctx context.Context, opts ...Option) *Group[T] {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	baseCtx, cancel := context.WithCancelCause(ctx)
	eg, runCtx := errgroup.WithContext(baseCtx)

	if cfg.maxConcurrency > 0 {
		eg.SetLimit(cfg.maxConcurrency)
	}

	g := &Group[T]{
		ctx:     runCtx,
		baseCtx: baseCtx,
		cancel:  cancel,
		eg:      eg,
		cfg:     cfg,
		queue:   make([]Result[T], 0, cfg.resultBuffer),
		notify:  make(chan struct{}),
	}
	return g
}

// Context returns the group context passed to each task.
func (g *Group[T]) Context() context.Context {
	return g.ctx
}

// Cancel cancels the group with the given cause.
func (g *Group[T]) Cancel(err error) {
	if err == nil {
		err = context.Canceled
	}
	g.cancelOnce.Do(func() {
		g.cancel(err)
	})
}

// Close seals the group and prevents future Go calls.
func (g *Group[T]) Close() {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		g.signalLocked()
	}
	g.mu.Unlock()
}

// Go starts a task.
func (g *Group[T]) Go(fn TaskFunc[T]) error {
	if fn == nil {
		return ErrNilTask
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return ErrGroupClosed
	}
	g.pending++
	g.signalLocked()
	g.mu.Unlock()

	g.eg.Go(func() (retErr error) {
		var (
			value   T
			taskErr error
		)

		defer func() {
			g.finish(Result[T]{Value: value, Err: taskErr})

			if taskErr != nil && g.cfg.failFast {
				retErr = taskErr
			}
		}()

		if g.cfg.panicToError {
			defer func() {
				if r := recover(); r != nil {
					taskErr = fmt.Errorf("seoul: panic recovered: %v", r)
				}
			}()
		}

		value, taskErr = fn(g.ctx)
		return nil
	})

	return nil
}

// Next blocks until one task completes, the caller context ends, or the group is closed and drained.
func (g *Group[T]) Next(ctx context.Context) (res Result[T], ok bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		g.mu.Lock()
		if len(g.queue) > 0 {
			res = g.queue[0]
			g.queue = g.queue[1:]
			g.mu.Unlock()
			return res, true, nil
		}
		if g.closed && g.pending == 0 {
			g.mu.Unlock()
			return Result[T]{}, false, nil
		}
		notify := g.notify
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return Result[T]{}, false, ctx.Err()
		case <-notify:
		}
	}
}

// Wait waits for all currently started tasks and returns the first observed error.
func (g *Group[T]) Wait() error {
	waitErr := g.eg.Wait()

	g.mu.Lock()
	firstErr := g.firstErr
	g.mu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	if waitErr != nil {
		return waitErr
	}
	return context.Cause(g.baseCtx)
}

func (g *Group[T]) finish(res Result[T]) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if res.Err != nil {
		if g.firstErr == nil {
			g.firstErr = res.Err
		}
		if g.cfg.failFast {
			g.cancelOnce.Do(func() {
				g.cancel(res.Err)
			})
		}
	}

	g.queue = append(g.queue, res)
	g.pending--
	g.signalLocked()
}

func (g *Group[T]) signalLocked() {
	ch := g.notify
	g.notify = make(chan struct{})
	close(ch)
}
