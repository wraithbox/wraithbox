package termfilter

// WithoutBELLimit turns the BEL rate limit off. Only tests can: the
// fuzz target checks its properties with the limit off (C10-terminal,
// "Fuzzing"), and `wb` always filters with it on.
func WithoutBELLimit() Option {
	return func(f *Filter) { f.belLimit = false }
}
