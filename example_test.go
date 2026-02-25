package seoul_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/jaeyoung0509/seoul"
)

func ExampleGroup_basic() {
	g := seoul.New[int](context.Background())
	_ = g.Go(func(context.Context) (int, error) { return 42, nil })
	g.Close()

	res, ok, err := g.Next(context.Background())
	fmt.Println(ok, err == nil, res.Value, res.Err == nil)

	_, ok, err = g.Next(context.Background())
	fmt.Println(ok, err == nil)

	fmt.Println(g.Wait() == nil)
	// Output:
	// true true 42 true
	// false true
	// true
}

func ExampleGroup_failFastDisabled() {
	errBoom := errors.New("boom")
	g := seoul.New[int](context.Background(), seoul.WithFailFast(false))

	_ = g.Go(func(context.Context) (int, error) { return 0, errBoom })
	_ = g.Go(func(context.Context) (int, error) { return 7, nil })
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

	fmt.Printf("success=%d fail=%d\n", success, fail)
	fmt.Println(errors.Is(g.Wait(), errBoom))
	// Output:
	// success=1 fail=1
	// true
}
