package seoul_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jaeyoung0509/seoul"
)

func ExampleGroup_next() {
	// 1) Create group.
	g := seoul.New[int](context.Background(), seoul.WithFailFast(false))

	// 2) Submit tasks.
	errBoom := errors.New("boom")
	_ = g.Go(func(context.Context) (int, error) { return 0, errBoom })
	_ = g.Go(func(context.Context) (int, error) { return 42, nil })

	// 3) Seal submissions so Next can terminate with ok=false after drain.
	g.Close()

	// 4) Pull completions manually with Next.
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
			continue
		}
		success++
	}

	// 5) Wait reports final group error semantics.
	fmt.Printf("success=%d fail=%d\n", success, fail)
	fmt.Println(errors.Is(g.Wait(), errBoom))
	// Output:
	// success=1 fail=1
	// true
}

func ExampleGroup_stream() {
	// Stream is a range-friendly adapter over Next.
	g := seoul.New[int](context.Background(), seoul.WithFailFast(false))

	first := make(chan struct{})
	second := make(chan struct{})
	secondDone := make(chan struct{})

	_ = g.Go(func(context.Context) (int, error) {
		<-first
		<-secondDone
		return 1, nil
	})
	_ = g.Go(func(context.Context) (int, error) {
		<-second
		close(secondDone)
		return 2, nil
	})
	g.Close()

	s := g.Stream(context.Background())

	// Force completion order: second then first.
	close(first)
	close(second)

	for res := range s.C {
		fmt.Println(res.Value, res.Err == nil)
	}

	// Done carries one final stream/group error.
	fmt.Println(<-s.Done == nil)
	// Output:
	// 2 true
	// 1 true
	// true
}

func ExampleGroup_stream_contextCancel() {
	g := seoul.New[int](context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	s := g.Stream(ctx)

	count := 0
	for range s.C {
		count++
	}

	fmt.Println(count)
	fmt.Println(errors.Is(<-s.Done, context.DeadlineExceeded))
	// Output:
	// 0
	// true
}
