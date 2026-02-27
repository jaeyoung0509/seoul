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

func ExampleGroup_results() {
	// Results is a range-friendly adapter over Next.
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

	// Force completion order: second then first.
	close(first)
	close(second)

	for res := range g.Results(context.Background()) {
		fmt.Println(res.Value, res.Err == nil)
	}

	// Final status remains owner-managed via Wait.
	fmt.Println(g.Wait() == nil)
	// Output:
	// 2 true
	// 1 true
	// true
}

func ExampleGroup_results_contextCancel() {
	g := seoul.New[int](context.Background(), seoul.WithFailFast(false))
	release := make(chan struct{})

	_ = g.Go(func(ctx context.Context) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-release:
			return 7, nil
		}
	})

	streamCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	count := 0
	for range g.Results(streamCtx) {
		count++
	}

	fmt.Println(count)
	fmt.Println(errors.Is(streamCtx.Err(), context.DeadlineExceeded))
	fmt.Println(g.Context().Err() == nil)

	// Owner decides when to continue/close/cancel the group.
	close(release)
	g.Close()
	for range g.Results(context.Background()) {
	}
	fmt.Println(g.Wait() == nil)

	// Output:
	// 0
	// true
	// true
	// true
}
