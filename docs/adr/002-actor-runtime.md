# ADR-002: Actor-Style Runtime over errgroup Execution Engine

- Status: Proposed
- Date: 2026-02-24
- Issue: https://github.com/jaeyoung0509/seoul/issues/12
- Supersedes runtime details in: ADR-001 (API contract remains unchanged)

## Context

`seoul` already adopts `errgroup` for task execution (`Go`, cancellation propagation, concurrency limit).
However, internal coordination is still centered on shared state guarded by locks.

As feature complexity grows (`Next(ctx)` waiters, fail-fast split, panic boundary, leak-safe shutdown),
shared mutable state is harder to reason about and easier to regress.

We want:

- stable public API from ADR-001,
- Go-idiomatic execution on `errgroup`,
- and actor-style state ownership for deterministic semantics.

## Decision

Adopt a split architecture:

1. Execution engine: `errgroup`
- task lifecycle
- cooperative cancellation
- `SetLimit` for `WithMaxConcurrency`

2. State engine: single manager loop (actor)
- owns all mutable coordination state
- processes commands/events sequentially
- no external goroutine mutates manager-owned state directly

Public API stays unchanged:

```go
func New[T any](ctx context.Context, opts ...Option) *Group[T]
func (g *Group[T]) Go(fn TaskFunc[T]) error
func (g *Group[T]) Close()
func (g *Group[T]) Next(ctx context.Context) (Result[T], bool, error)
func (g *Group[T]) Wait() error
func (g *Group[T]) Cancel(err error)
```

## Runtime Model

### Internal Channels

- `cmdCh`: API calls to manager (`submit`, `close`, `next`, `wait`, `cancel`)
- `evtCh`: execution events from worker wrappers (`taskDone`)
- `doneCh`: manager shutdown signal (optional, implementation detail)

### Command Types

- `submit(fn, replyCh error)`
- `close(replyCh struct{})`
- `next(ctx, replyCh nextReply)`
- `wait(replyCh error)`
- `cancel(cause, replyCh struct{})`

### Event Types

- `taskDone(result)` where `result` includes `{value, err}`

### Manager-Owned State

- `closed bool`
- `inflight int`
- `results []Result[T]`
- `nextWaiters []next waiter`
- `firstErr error`
- `waiters []wait reply channel`
- `terminal bool` (derived; optional cache)

## State Invariants

1. `inflight >= 0`
2. Only manager loop mutates `closed`, `inflight`, `results`, `firstErr`, waiter lists.
3. A submitted task increments `inflight` exactly once before dispatch.
4. Every accepted task emits exactly one `taskDone` event.
5. On each `taskDone`, manager decrements `inflight` exactly once.
6. `firstErr` captures earliest observed task error and never changes afterward.
7. Terminal drain condition is true iff:
- `closed == true`
- `inflight == 0`
- `len(results) == 0`

## API Semantics Mapping

### `Go`

- sends `submit` command
- if `closed == true`: returns `ErrGroupClosed`
- otherwise accepted and task starts via `errgroup.Go`

### `Close`

- sends `close` command
- idempotent
- prevents future accepts but does not cancel running tasks

### `Next(ctx)`

- if `results` has entries: pop completion-order head and return `(res, true, nil)`
- if terminal drain condition holds: return `(zero, false, nil)`
- otherwise register waiter and block until:
  - result available -> `(res, true, nil)`
  - caller ctx canceled/deadline -> `(zero, false, ctx.Err())`
  - terminal drain -> `(zero, false, nil)`

### `Wait`

- waits for `errgroup.Wait()` completion path via manager coordination
- return priority:
  1. `firstErr` (if any)
  2. group context cause (if any)
  3. `nil`

### `Cancel(err)`

- sends `cancel` command (or directly cancels group context if that remains outside manager scope)
- `nil` cause maps to `context.Canceled`

## Fail-Fast and Panic Policies

### Fail-Fast

- `fail-fast=true`: first `taskDone` with error triggers group cancel.
- `fail-fast=false`: no automatic cancel; task errors still streamed via `Next`.

### Panic-to-Error

- `panic-to-error=true`: worker wrapper recovers panic and emits `taskDone{Err: panicErr}`.
- `panic-to-error=false`: panic escapes wrapper per policy (documented behavior).

Manager must preserve invariants in both modes.

## Pseudocode (Non-Normative)

```go
for {
    select {
    case cmd := <-cmdCh:
        handleCommand(cmd)
    case evt := <-evtCh:
        handleEvent(evt)
    }
}
```

`handleEvent(taskDone)` is responsible for:

- appending result
- setting `firstErr` if needed
- canceling on fail-fast policy
- waking pending `Next` waiters
- resolving `Wait` waiters on completion

## Consequences

- Coordination logic is easier to audit than lock-based multi-writer state.
- `Next(ctx)` and terminal behavior become explicit state transitions.
- Future features (multi-error collection, backpressure policies) can be added as manager commands/events.

Tradeoff:

- more internal plumbing (command/event structs, manager goroutine).

## Rollout Plan

1. Implement fail-fast policy on actor loop: issue #4
2. Integrate panic boundary with actor events: issue #5
3. Harden tests for actor invariants/race/leak: issue #6
4. Update docs/examples to new architecture narrative: issue #7
5. Benchmark actor runtime vs plain errgroup+channel: issue #8
