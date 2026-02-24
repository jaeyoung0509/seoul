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
		g.Go(func(ctx context.Context) (int, error) {
			time.Sleep(time.Duration(3-i) * 50 * time.Millisecond)
			return i, nil
		})
	}

	for {
		res, ok := g.Next()
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
- `(*Group[T]).Go(fn TaskFunc[T])`
- `(*Group[T]).Next() (Result[T], bool)`
- `(*Group[T]).Wait() error`
- `(*Group[T]).Cancel(err error)`

## Notes

- `Next` blocks until a task finishes.
- `Next` returns `ok=false` when there are no queued results and no running tasks.
- `Wait` returns the first observed task error (if any), otherwise context cancellation cause.

## Status

MVP in progress. Planned next:

- richer error policy (collect all errors)
- explicit close/seal semantics
- examples and benchmarks vs plain `errgroup`
