package seoul

// Option configures a Group.
type Option func(*config)

type config struct {
	failFast       bool
	maxConcurrency int
	panicToError   bool
	resultBuffer   int
}

func defaultConfig() config {
	return config{
		failFast:     true,
		panicToError: true,
	}
}

// WithFailFast cancels the group when the first error is observed.
func WithFailFast(enabled bool) Option {
	return func(c *config) {
		c.failFast = enabled
	}
}

// WithMaxConcurrency limits how many tasks can execute at the same time.
// 0 means unlimited.
func WithMaxConcurrency(limit int) Option {
	if limit < 0 {
		panic("seoul: max concurrency cannot be negative")
	}

	return func(c *config) {
		c.maxConcurrency = limit
	}
}

// WithPanicToError converts task panics to errors.
func WithPanicToError(enabled bool) Option {
	return func(c *config) {
		c.panicToError = enabled
	}
}

// WithResultBuffer preallocates result queue capacity.
func WithResultBuffer(size int) Option {
	if size < 0 {
		panic("seoul: result buffer cannot be negative")
	}

	return func(c *config) {
		c.resultBuffer = size
	}
}
