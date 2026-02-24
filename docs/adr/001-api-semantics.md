# ADR-001: seoul v0.1 API and Semantics

- Status: Proposed
- Date: 2026-02-24
- Issue: https://github.com/jaeyoung0509/seoul/issues/1

## Context

`golang.org/x/sync/errgroup` is strong at cooperative cancellation and concurrency limits, but does not provide completion-order result streaming (`join_next` style). `seoul` should stay small and Go-idiomatic while adding exactly that missing layer.

We need a stable v0.1 contract before further runtime work.

## Decision

### Public API (v0.1)

```go
type TaskFunc[T any] func(context.Context) (T, error)

type Result[T any] struct {
    Value T
    Err   error
}

type Group[T any]

func New[T any](ctx context.Context, opts ...Option) *Group[T]
func (g *Group[T]) Go(fn TaskFunc[T]) error
func (g *Group[T]) Close()
func (g *Group[T]) Next(ctx context.Context) (res Result[T], ok bool, err error)
func (g *Group[T]) Wait() error
func (g *Group[T]) Cancel(err error)
```

### Option Set (v0.1)

- `WithFailFast(bool)`
- `WithMaxConcurrency(int)`
- `WithPanicToError(bool)`

Defaults:
- `fail-fast = true`
- `panic-to-error = true`
- `max-concurrency = 0` (unlimited)

### Method Semantics

| Method | Behavior |
|---|---|
| `Go` | Registers and starts one task. Returns error if group is already closed. |
| `Close` | Seals task submission. Idempotent. Running tasks continue. |
| `Next(ctx)` | Returns one completed task result in completion order. Blocks if no result is currently queued. |
| `Wait` | Waits until all accepted tasks are done. Returns first observed task error if any; otherwise context cancellation cause if any; otherwise `nil`. |
| `Cancel(err)` | Cancels group context with cause (uses `context.Canceled` if `err == nil`). |

### `Next(ctx)` Return Contract

`Next(ctx)` returns `(res, ok, err)` with the following rules:

- `(res, true, nil)`: one task completion was dequeued.
- `(zero, false, nil)`: terminal drain condition only.
  - This only happens when:
    - group is closed,
    - all accepted tasks finished,
    - and result queue is empty.
- `(zero, false, err)`: caller context ended while waiting (`context.DeadlineExceeded` or `context.Canceled`).

### Failure Policy Matrix

| Policy | On first task error | Effect on other tasks | `Next(ctx)` | `Wait()` |
|---|---|---|---|---|
| `fail-fast=true` (default) | Group context is canceled immediately | cooperative stop (task code observes context) | still yields completed/errored results in completion order | returns first task error |
| `fail-fast=false` | no automatic group cancellation | other tasks continue normally | yields all results, including errors | returns first task error after all tasks complete |

Notes:
- `fail-fast` controls cancellation behavior, not whether errors are reported.
- If there is no task error but parent context is canceled, `Wait()` returns that context cause.

### Panic Policy

Default is `WithPanicToError(true)`.

- `true`: recover panic from task boundary and convert to `error` (recorded in `Result.Err`, participates in `Wait` first-error rule).
- `false`: panic escapes task boundary (debug-first behavior).

Reason for default `true`:
- library consumers get predictable task-lifecycle handling,
- panic from one task does not silently strand queue draining logic,
- operational systems usually prefer error surfaces over process-level crashes by default.

## Consequences

- Runtime implementation can use `errgroup` as execution engine while `seoul` owns result-queue semantics.
- API stays small; advanced features (multi-error collection, priority queues, retries) are explicitly out of v0.1.
- Tests must prove ordering, drain condition, fail-fast split, panic conversion, and concurrency limit behavior.

## Out of Scope

- Multi-error aggregate API
- Retry/backoff orchestration
- Distributed queue or pub/sub abstractions

## Rollout Gate

Subsequent implementation issues (`#2` and later) SHOULD start only after maintainers review and accept this ADR.
