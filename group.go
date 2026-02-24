package seoul

import (
	"context"
	"fmt"
	"sync"
)

// TaskFunc is a unit of work managed by Group.
type TaskFunc[T any] func(context.Context) (T, error)

// Result is returned by Next in task-completion order.
type Result[T any] struct {
	Value T
	Err   error
}

// Group runs tasks and streams completed results with Next.
type Group[T any] struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	cfg    config

	sem chan struct{}

	mu       sync.Mutex
	cond     *sync.Cond
	pending  int
	queue    []Result[T]
	firstErr error

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

	runCtx, cancel := context.WithCancelCause(ctx)
	g := &Group[T]{
		ctx:    runCtx,
		cancel: cancel,
		cfg:    cfg,
		queue:  make([]Result[T], 0, cfg.resultBuffer),
	}
	if cfg.maxConcurrency > 0 {
		g.sem = make(chan struct{}, cfg.maxConcurrency)
	}
	g.cond = sync.NewCond(&g.mu)
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

// Go starts a task.
func (g *Group[T]) Go(fn TaskFunc[T]) {
	if fn == nil {
		panic("seoul: nil task")
	}

	g.mu.Lock()
	g.pending++
	g.mu.Unlock()

	go g.run(fn)
}

// Next blocks until one task completes, or returns ok=false if the group is empty.
func (g *Group[T]) Next() (res Result[T], ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for {
		if len(g.queue) > 0 {
			res = g.queue[0]
			g.queue = g.queue[1:]
			return res, true
		}
		if g.pending == 0 {
			return Result[T]{}, false
		}
		g.cond.Wait()
	}
}

// Wait waits for all currently started tasks and returns the first observed error.
func (g *Group[T]) Wait() error {
	g.mu.Lock()
	for g.pending > 0 {
		g.cond.Wait()
	}
	firstErr := g.firstErr
	g.mu.Unlock()

	if firstErr != nil {
		return firstErr
	}
	return context.Cause(g.ctx)
}

func (g *Group[T]) run(fn TaskFunc[T]) {
	var (
		value T
		err   error
	)

	if !g.acquire() {
		cancelErr := context.Cause(g.ctx)
		if cancelErr == nil {
			cancelErr = context.Canceled
		}
		g.finish(Result[T]{Err: cancelErr})
		return
	}
	defer g.release()

	if g.cfg.panicToError {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("seoul: panic recovered: %v", r)
			}
			g.finish(Result[T]{Value: value, Err: err})
		}()
	} else {
		defer func() {
			g.finish(Result[T]{Value: value, Err: err})
		}()
	}

	value, err = fn(g.ctx)
}

func (g *Group[T]) acquire() bool {
	if g.sem == nil {
		return true
	}

	select {
	case g.sem <- struct{}{}:
		return true
	case <-g.ctx.Done():
		return false
	}
}

func (g *Group[T]) release() {
	if g.sem == nil {
		return
	}
	<-g.sem
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
	g.cond.Broadcast()
}
