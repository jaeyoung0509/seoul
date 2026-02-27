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

type nextReply[T any] struct {
	res Result[T]
	ok  bool
	err error
}

type taskDoneEvent[T any] struct {
	res Result[T]
}

type submitCmd[T any] struct {
	resp chan error
}

type closeCmd struct {
	resp chan struct{}
}

type nextCmd[T any] struct {
	resp chan nextReply[T]
}

type cancelNextCmd[T any] struct {
	resp chan nextReply[T]
	err  error
}

type firstErrCmd struct {
	resp chan error
}

type managerState[T any] struct {
	closed      bool
	inflight    int
	results     []Result[T]
	nextWaiters []chan nextReply[T]
	firstErr    error
}

// Group runs tasks and exposes completed results with Next/Results.
type Group[T any] struct {
	ctx     context.Context
	baseCtx context.Context
	cancel  context.CancelCauseFunc
	eg      *errgroup.Group
	cfg     config

	cmdCh chan any
	evtCh chan taskDoneEvent[T]

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
		cmdCh:   make(chan any),
		evtCh:   make(chan taskDoneEvent[T]),
	}
	go g.runManager()

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
	resp := make(chan struct{}, 1)
	g.cmdCh <- closeCmd{resp: resp}
	<-resp
}

// Go starts a task.
func (g *Group[T]) Go(fn TaskFunc[T]) error {
	if fn == nil {
		return ErrNilTask
	}

	resp := make(chan error, 1)
	g.cmdCh <- submitCmd[T]{resp: resp}
	if err := <-resp; err != nil {
		return err
	}

	g.eg.Go(func() (retErr error) {
		var (
			value   T
			taskErr error
		)

		defer func() {
			g.evtCh <- taskDoneEvent[T]{res: Result[T]{Value: value, Err: taskErr}}

			if taskErr != nil && g.cfg.failFast {
				g.cancelOnce.Do(func() {
					g.cancel(taskErr)
				})
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
		return
	})

	return nil
}

// Next blocks until one task completes, the caller context ends, or the group is closed and drained.
func (g *Group[T]) Next(ctx context.Context) (res Result[T], ok bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	resp := make(chan nextReply[T], 1)
	g.cmdCh <- nextCmd[T]{resp: resp}

	stopCancelWatcher := make(chan struct{})
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				g.cmdCh <- cancelNextCmd[T]{resp: resp, err: ctx.Err()}
			case <-stopCancelWatcher:
			}
		}()
	}

	reply := <-resp
	close(stopCancelWatcher)
	return reply.res, reply.ok, reply.err
}

// Results adapts Next(ctx) into a range-friendly results channel.
//
// Results observes task completion in the same completion order as Next.
// The returned channel closes when:
//   - Next(ctx) returns ok=false (group closed and drained), or
//   - Next(ctx) returns err!=nil (typically caller context ended).
//
// Results never calls Close, Cancel, or Wait. Group lifecycle remains owner-managed.
func (g *Group[T]) Results(ctx context.Context) <-chan Result[T] {
	if ctx == nil {
		ctx = context.Background()
	}

	out := make(chan Result[T])
	go func() {
		defer close(out)
		for {
			res, ok, err := g.Next(ctx)
			if err != nil || !ok {
				return
			}

			select {
			case out <- res:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// Wait waits for all currently started tasks and returns the first observed error.
func (g *Group[T]) Wait() error {
	waitErr := g.eg.Wait()
	firstErr := g.getFirstErr()

	if firstErr != nil {
		return firstErr
	}
	if waitErr != nil {
		return waitErr
	}
	return context.Cause(g.baseCtx)
}

func (g *Group[T]) getFirstErr() error {
	resp := make(chan error, 1)
	g.cmdCh <- firstErrCmd{resp: resp}
	return <-resp
}

func (g *Group[T]) runManager() {
	state := managerState[T]{
		results:     make([]Result[T], 0, g.cfg.resultBuffer),
		nextWaiters: make([]chan nextReply[T], 0),
	}

	for {
		select {
		case raw := <-g.cmdCh:
			switch cmd := raw.(type) {
			case submitCmd[T]:
				if state.closed {
					cmd.resp <- ErrGroupClosed
					continue
				}
				state.inflight++
				cmd.resp <- nil

			case closeCmd:
				state.closed = true
				g.drainIfTerminal(&state)
				cmd.resp <- struct{}{}

			case nextCmd[T]:
				if len(state.results) > 0 {
					res := state.results[0]
					state.results = state.results[1:]
					cmd.resp <- nextReply[T]{res: res, ok: true}
					continue
				}
				if state.closed && state.inflight == 0 {
					cmd.resp <- nextReply[T]{ok: false}
					continue
				}
				state.nextWaiters = append(state.nextWaiters, cmd.resp)

			case cancelNextCmd[T]:
				idx := indexOfWaiter(state.nextWaiters, cmd.resp)
				if idx == -1 {
					continue
				}
				state.nextWaiters = removeWaiter(state.nextWaiters, idx)
				cmd.resp <- nextReply[T]{ok: false, err: cmd.err}

			case firstErrCmd:
				cmd.resp <- state.firstErr
			}

		case evt := <-g.evtCh:
			if state.firstErr == nil && evt.res.Err != nil {
				state.firstErr = evt.res.Err
			}
			if state.inflight > 0 {
				state.inflight--
			}

			if len(state.nextWaiters) > 0 {
				waiter := state.nextWaiters[0]
				state.nextWaiters = state.nextWaiters[1:]
				waiter <- nextReply[T]{res: evt.res, ok: true}
			} else {
				state.results = append(state.results, evt.res)
			}
			g.drainIfTerminal(&state)
		}
	}
}

func (g *Group[T]) drainIfTerminal(state *managerState[T]) {
	if !state.closed || state.inflight != 0 || len(state.results) != 0 {
		return
	}
	for _, waiter := range state.nextWaiters {
		waiter <- nextReply[T]{ok: false}
	}
	state.nextWaiters = state.nextWaiters[:0]
}

func indexOfWaiter[T any](waiters []chan nextReply[T], target chan nextReply[T]) int {
	for i, waiter := range waiters {
		if waiter == target {
			return i
		}
	}
	return -1
}

func removeWaiter[T any](waiters []chan nextReply[T], idx int) []chan nextReply[T] {
	copy(waiters[idx:], waiters[idx+1:])
	waiters[len(waiters)-1] = nil
	return waiters[:len(waiters)-1]
}
