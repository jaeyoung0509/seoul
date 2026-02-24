# seoul

`seoul` is a tiny Go concurrency helper inspired by Tokio-style `JoinSet` usage:

- start many tasks
- consume results in completion order (`Next`)
- keep simple fail-fast cancellation (`WithFailFast(true)`)

## Install

```bash
go get github.com/jaeyoung0509/seoul
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jaeyoung0509/seoul"
)

func main() {
	g := seoul.New[int](context.Background(), seoul.WithMaxConcurrency(4))

	for i := 0; i < 3; i++ {
		i := i
		if err := g.Go(func(ctx context.Context) (int, error) {
			time.Sleep(time.Duration(3-i) * 50 * time.Millisecond)
			return i, nil
		}); err != nil {
			panic(err)
		}
	}

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

## API

- `New[T any](ctx context.Context, opts ...Option) *Group[T]`
- `(*Group[T]).Go(fn TaskFunc[T]) error`
- `(*Group[T]).Close()`
- `(*Group[T]).Next(ctx context.Context) (Result[T], bool, error)`
- `(*Group[T]).Wait() error`
- `(*Group[T]).Cancel(err error)`

## Notes

- `Next(ctx)` blocks until a task finishes, context ends, or group is closed+drained.
- `Next(ctx)` returns `ok=false, err=nil` only when group is closed and drained.
- `Wait` returns the first observed task error (if any), otherwise context cancellation cause.

## Status

MVP in progress. Planned next:

- richer error policy (collect all errors)
- explicit close/seal semantics
- examples and benchmarks vs plain `errgroup`
