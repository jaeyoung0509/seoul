# seoul

`seoul` is a small Go concurrency helper inspired by Tokio-style `JoinSet` ergonomics.

It is built as:
- `errgroup` for task execution/cancellation/limit
- an internal actor-style manager loop for result ordering and state consistency

## Install

```bash
go get github.com/jaeyoung0509/seoul
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jaeyoung0509/seoul"
)

func main() {
	g := seoul.New[int](
		context.Background(),
		seoul.WithMaxConcurrency(4),
	)

	for i := 0; i < 3; i++ {
		i := i
		if err := g.Go(func(context.Context) (int, error) {
			time.Sleep(time.Duration(3-i) * 30 * time.Millisecond)
			return i, nil
		}); err != nil {
			panic(err)
		}
	}

	// Required for terminal drain (`ok=false, err=nil`).
	g.Close()

	for {
		res, ok, err := g.Next(context.Background())
		if err != nil {
			panic(err)
		}
		if !ok {
			break
		}
		fmt.Println("done:", res.Value, "err:", res.Err)
	}

	if err := g.Wait(); err != nil {
		panic(err)
	}
}
```

## Example: fail-fast off

```go
errBoom := errors.New("boom")
g := seoul.New[int](context.Background(), seoul.WithFailFast(false))

_ = g.Go(func(context.Context) (int, error) { return 0, errBoom })
_ = g.Go(func(context.Context) (int, error) { return 42, nil })
g.Close()

var success, fail int
for {
	res, ok, err := g.Next(context.Background())
	if err != nil {
		panic(err)
	}
	if !ok {
		break
	}
	if res.Err != nil {
		fail++
	} else {
		success++
	}
}
```

## API

- `New[T any](ctx context.Context, opts ...Option) *Group[T]`
- `(*Group[T]).Go(fn TaskFunc[T]) error`
- `(*Group[T]).Close()`
- `(*Group[T]).Next(ctx context.Context) (Result[T], bool, error)`
- `(*Group[T]).Wait() error`
- `(*Group[T]).Cancel(err error)`

## Semantics

- `Next(ctx)` returns one completion in completion order.
- `Next(ctx)` returns `ok=false, err=nil` only when `Close()` was called and results are fully drained.
- `Next(ctx)` returns `ok=false, err=context.*` when caller context ends while waiting.
- `WithFailFast(true)` cancels group context on first task error.
- `WithFailFast(false)` keeps remaining tasks running and still reports errors via results and `Wait`.
- `WithPanicToError(true)` (default) converts panic to task error.
- `WithPanicToError(false)` rethrows panic (debug-first behavior).
- `Wait()` returns first observed task error, else context cause, else `nil`.

## Testing

```bash
go test ./...
go test -race ./...
```

- Use timeout-bounded contexts in `Next(ctx)` tests to avoid hangs.
- Prefer channel synchronization over long `sleep` values to reduce flakiness.
- For panic rethrow behavior, assert in a subprocess test helper.

## Benchmarks

- Command: `go test -run '^$' -bench . -benchmem ./...`
- Latest numbers and interpretation: [`docs/benchmarks.md`](docs/benchmarks.md)

## Status

MVP. Current focus is reducing overhead while preserving actor-runtime semantics.
