package termfilter

// WithoutBELLimit turns the BEL rate limit off. Only tests can: the
// fuzz target checks its properties with the limit off (C10-terminal,
// "Fuzzing"), and `wb` always filters with it on.
func WithoutBELLimit() Option {
	return func(f *Filter) { f.belLimit = false }
}

// Model is the terminal state model of model_test.go, for the relay's
// C10-terminal conformance test in package termfilter_test, which can
// import internal/relay where this package can't.
type Model struct{ m *termModel }

// NewModel returns a model in its initial state.
func NewModel() Model { return Model{newModel()} }

// Feed replays p into the model.
func (m Model) Feed(p []byte) { m.m.feed(p) }

// Changed returns the states that differ from the initial state.
func (m Model) Changed() []string { return m.m.changed() }

// Title returns the window title.
func (m Model) Title() string { return m.m.title }
