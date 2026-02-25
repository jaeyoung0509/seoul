// Package seoul provides a small task-group abstraction for Go concurrency.
//
// It combines:
//   - errgroup for task execution, cancellation, and concurrency limits
//   - an internal actor-style manager loop for ordered result streaming
//
// Core behavior:
//   - submit tasks with Go
//   - consume completions in completion order via Next(ctx)
//   - stop new submissions with Close
//   - wait for completion with Wait
//
// Semantics:
//   - Next(ctx) returns (res, true, nil) for one completed task
//   - Next(ctx) returns (zero, false, nil) only after Close and full drain
//   - Next(ctx) returns (zero, false, ctx.Err()) if caller context ends
//   - Wait returns the first observed task error, then context cause, then nil
//
// Policy options:
//   - WithFailFast(true): cancel group on first task error
//   - WithFailFast(false): keep remaining tasks running
//   - WithPanicToError(true): convert panic to task error (default)
//   - WithPanicToError(false): rethrow panic
package seoul
