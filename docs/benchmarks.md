# Benchmarks

Date: 2026-02-25

Command:

```bash
go test -run '^$' -bench . -benchmem ./...
```

Environment:

- `goos`: `darwin`
- `goarch`: `arm64`
- `cpu`: `Apple M1`
- package: `github.com/jaeyoung0509/seoul`

## Results

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkSeoul/short/failfast_off` | 395640 | 143153 | 2081 |
| `BenchmarkSeoul/short/failfast_on` | 394070 | 143161 | 2082 |
| `BenchmarkSeoul/mixed/failfast_off` | 476993 | 151118 | 2185 |
| `BenchmarkSeoul/mixed/failfast_on` | 394318 | 143273 | 2083 |
| `BenchmarkErrgroupChannel/short/failfast_off` | 101604 | 34274 | 527 |
| `BenchmarkErrgroupChannel/short/failfast_on` | 105336 | 34269 | 527 |
| `BenchmarkErrgroupChannel/mixed/failfast_off` | 291319 | 42216 | 623 |
| `BenchmarkErrgroupChannel/mixed/failfast_on` | 108299 | 34415 | 529 |

## Interpretation

- Plain `errgroup+channel` has lower overhead in this microbenchmark (time + allocations).
- `seoul` pays extra coordination cost for stronger runtime semantics:
  - completion-order streaming with `Next(ctx)`
  - terminal drain contract (`Close` + drained queue)
  - actor-managed state consistency
- `failfast_on` for both implementations tends to be faster in mixed workloads because early error short-circuits work.

## Notes

- These numbers are point-in-time and machine-dependent.
- Re-run the command on your target environment before making performance decisions.
