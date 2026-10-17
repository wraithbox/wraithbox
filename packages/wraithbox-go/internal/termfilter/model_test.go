package termfilter

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// The terminal state model of C10-terminal, "Fuzzing". The exit reset
// property replays the filter's output and the reset into it. It is a
// model and not a terminal emulator: it keeps the states that
// S16-03-around-the-stream, "Reset on exit", lists, and nothing it
// doesn't need, such as the cursor position or the cells. The emulator
// X19-terminal-filter used, charmbracelet/x/vt, tracks neither the kitty
// keyboard stacks nor modifyOtherKeys.
//
// A token the model doesn't know is recorded under unknown, which is
// never cleared, so a model that misses a token the filter passes fails
// the property rather than pass it unseen.

// userFlags marks the kitty keyboard entry the user's shell pushed
// before the session. A pop that reaches it removes it, and the main
// stack then differs from the initial one.
const userFlags = -1

// kittyStackSize is how many entries one kitty keyboard stack holds in
// kitty. A push past it evicts the oldest entry, as the protocol says.
const kittyStackSize = 8

// sgrState is the style SGR sets (decision 5 of #301): every attribute
// with its partial reset. Codes the model doesn't name set other, which
// only `CSI 0 m` clears.
type sgrState struct {
	bold, faint, italic, fraktur bool
	inverse, hidden, strike      bool
	overline, proportional       bool
	underline                    int // 0 none, 1 single, 2 double, 3 to 5 the styles of `4:n`
	blink                        int // 0, 5 or 6
	font                         int // 0, or 11 to 19
	framed                       int // 0, 51 or 52
	ideogram                     int // 0, or 60 to 64
	script                       int // 0, 73 or 74
	fg, bg, ul                   string
	other                        bool
}

// savedCursor is what DECSC saves and DECRC restores, of the states the
// model keeps: the style and the character sets.
type savedCursor struct {
	sgr    sgrState
	g0, g1 byte
	gl     int
}

// termState is every state the property compares.
type termState struct {
	// modes holds the private modes whose value differs from the
	// default. Every mode is reset by default, except 25, which is set.
	modes map[int]bool
	alt   bool
	sgr   sgrState
	// cursorStyle is the number of `CSI n SP q`, 0 for the default, -1
	// for a form the model doesn't read.
	cursorStyle int
	// scroll holds the top and bottom margins, or is empty for the
	// whole screen.
	scroll string
	g0, g1 byte
	gl     int
	// keypadApp is set by `ESC =` and reset by `ESC >`.
	keypadApp bool
	// keyResources holds the `CSI > Pp ; Pv m` values, modifyOtherKeys
	// (4) among them. A resource without a value is at its default.
	keyResources map[int]string
	// tabsChanged is set by a change to the tab stops, and reset by
	// `CSI ? 5 W`, which sets one every 8 columns.
	tabsChanged bool
	kitty       [2][]int // main, alternate
	// progress is the state of the last OSC 9 ; 4 report, 0 for none.
	progress int
	// status holds the ids of the open program status reports, "" for
	// a report without an id.
	status  map[string]bool
	unknown []string
}

// termModel is the state and what DECSC saved on each screen.
type termModel struct {
	termState
	saved [2]savedCursor
}

func newModel() *termModel {
	m := &termModel{termState: initialState()}
	m.saved[0] = m.cursor()
	m.saved[1] = m.cursor()
	return m
}

func initialState() termState {
	return termState{
		modes:        map[int]bool{},
		g0:           'B',
		g1:           'B',
		keyResources: map[int]string{},
		kitty:        [2][]int{{userFlags}, {}},
		status:       map[string]bool{},
	}
}

// diff returns the names of the states in which a and b differ.
func diff(a, b *termState) []string {
	checks := []struct {
		name string
		same bool
	}{
		{"modes", maps.Equal(a.modes, b.modes)},
		{"screen", a.alt == b.alt},
		{"style", a.sgr == b.sgr},
		{"cursor-style", a.cursorStyle == b.cursorStyle},
		{"scroll-region", a.scroll == b.scroll},
		{"g0", a.g0 == b.g0},
		{"g1", a.g1 == b.g1},
		{"gl", a.gl == b.gl},
		{"keypad", a.keypadApp == b.keypadApp},
		{"key-resources", maps.Equal(a.keyResources, b.keyResources)},
		{"tab-stops", a.tabsChanged == b.tabsChanged},
		{"kitty-main", slices.Equal(a.kitty[0], b.kitty[0])},
		{"kitty-alt", slices.Equal(a.kitty[1], b.kitty[1])},
		{"progress", a.progress == b.progress},
		{"status", maps.Equal(a.status, b.status)},
		{"unknown", slices.Equal(a.unknown, b.unknown)},
	}
	var names []string
	for _, c := range checks {
		if !c.same {
			names = append(names, c.name)
		}
	}
	return names
}

// changed returns the states that differ from the initial state, each
// with its value.
func (m *termModel) changed() []string {
	init := initialState()
	names := diff(&m.termState, &init)
	for i, n := range names {
		names[i] = fmt.Sprintf("%s (%+v)", n, m.value(n))
	}
	return names
}

func (m *termModel) value(name string) any {
	switch name {
	case "modes":
		return m.modes
	case "style":
		return m.sgr
	case "kitty-main":
		return m.kitty[0]
	case "kitty-alt":
		return m.kitty[1]
	case "status":
		return m.status
	case "unknown":
		return m.unknown
	}
	return fmt.Sprintf("%+v", m.termState)
}

// feed replays p into the model.
func (m *termModel) feed(p []byte) {
	var tok tokenizer
	tok.feed(p, m.token)
	tok.end(m.token)
}

func (m *termModel) token(t token) {
	switch t.kind {
	case kText, kInvalid:
	case kControl:
		m.control(t.raw[0])
	case kESC:
		m.escape(string(t.raw[1:]), t.cut)
	case kCSI:
		m.csi(t)
	case kOSC:
		m.osc(t)
	case kC1, kDCS, kSOS, kPM, kAPC:
		m.unknownToken(t.raw)
	}
}

func (m *termModel) unknownToken(raw []byte) {
	m.unknown = append(m.unknown, string(raw))
}

func (m *termModel) control(b byte) {
	switch b {
	case 0x0e: // SO invokes G1.
		m.gl = 1
	case 0x0f: // SI invokes G0.
		m.gl = 0
	}
}

func (m *termModel) escape(s string, cut bool) {
	if cut {
		m.unknownToken([]byte("\x1b" + s))
		return
	}
	switch {
	case s == "7":
		m.saveCursor()
	case s == "8":
		m.restoreCursor()
	case s == "M", s == "D", s == "E":
	case s == "=":
		m.keypadApp = true
	case s == ">":
		m.keypadApp = false
	case len(s) == 2 && s[0] == '(':
		m.g0 = s[1]
	case len(s) == 2 && s[0] == ')':
		m.g1 = s[1]
	case s == "H": // HTS sets a tab stop.
		m.tabsChanged = true
	default:
		m.unknownToken([]byte("\x1b" + s))
	}
}

func (m *termModel) screen() int {
	if m.alt {
		return 1
	}
	return 0
}

func (m *termModel) cursor() savedCursor {
	return savedCursor{sgr: m.sgr, g0: m.g0, g1: m.g1, gl: m.gl}
}

func (m *termModel) saveCursor() {
	m.saved[m.screen()] = m.cursor()
}

func (m *termModel) restoreCursor() {
	c := m.saved[m.screen()]
	m.sgr, m.g0, m.g1, m.gl = c.sgr, c.g0, c.g1, c.gl
}

// csiNoState are the CSI final bytes, without a prefix or an
// intermediate, that change none of the states the model keeps.
const csiNoState = "ABCDEFGHf`ade" + // cursor moves
	"@LMPXb" + // inserts, deletes, erases and repeats
	"ST" + // scrolls
	"IZ" + // tab moves
	"JK" + // erases in display and line
	"cnt" // queries

// csiQueries are the other CSI forms that change none of the states the
// model keeps: the mode query, the kitty keyboard query, and the
// version queries.
var csiQueries = map[csiKey]bool{
	{prefix: '?', inter: "$", final: 'p'}: true,
	{prefix: '?', final: 'u'}:             true,
	{prefix: '>', final: 'c'}:             true,
	{prefix: '>', final: 'q'}:             true,
}

func (m *termModel) csi(t token) {
	c, ok := parseCSI(t.raw)
	if t.cut || !ok {
		m.unknownToken(t.raw)
		return
	}
	key := csiKey{prefix: c.prefix, inter: c.inter, final: c.final}
	if csiQueries[key] || (key == csiKey{final: c.final} && strings.IndexByte(csiNoState, c.final) >= 0) {
		return
	}
	if !m.csiState(c) {
		m.unknownToken(t.raw)
	}
}

// csiState applies a CSI that may change a state, and reports whether
// the model knows it.
func (m *termModel) csiState(c csiForm) bool {
	switch key := (csiKey{prefix: c.prefix, inter: c.inter, final: c.final}); key {
	case csiKey{final: 'm'}:
		m.sgr.apply(c.params)
	case csiKey{final: 'r'}:
		m.scrollRegion(c.params)
	case csiKey{final: 'g'}:
		m.tabsChanged = true
	case csiKey{final: 's'}, csiKey{final: 'u'}:
		return c.params == "" && m.scoSave(c.final)
	case csiKey{prefix: '?', final: 'h'}, csiKey{prefix: '?', final: 'l'}:
		return m.setModes(c.params, c.final == 'h')
	case csiKey{prefix: '?', final: 'W'}:
		// DECST8C: a tab stop every 8 columns.
		m.tabsChanged = m.tabsChanged && c.params != "5"
	case csiKey{prefix: '>', final: 'u'}:
		m.kittyPush(c.params)
	case csiKey{prefix: '<', final: 'u'}:
		return m.kittyPop(c.params)
	case csiKey{prefix: '>', final: 'm'}:
		return m.keyResource(c.params)
	case csiKey{inter: " ", final: 'q'}:
		m.cursorStyle = numberOr(c.params, 0)
	case csiKey{inter: "!", final: 'p'}:
		m.softReset()
	default:
		return false
	}
	return true
}

// scoSave is `CSI s` and `CSI u`, which xterm treats as DECSC and DECRC.
func (m *termModel) scoSave(final byte) bool {
	if final == 's' {
		m.saveCursor()
	} else {
		m.restoreCursor()
	}
	return true
}

// numberOr reads params as one number, def for none, and -1 for a form
// the model doesn't read.
func numberOr(params string, def int) int {
	if params == "" {
		return def
	}
	n, ok := number(params)
	if !ok {
		return -1
	}
	return n
}

// scrollRegion is DECSTBM. A top of 0 or 1 and no bottom is the whole
// screen. The model keeps any other form as it came, an invalid one
// too, which a terminal may ignore.
func (m *termModel) scrollRegion(params string) {
	top, bottom, _ := strings.Cut(params, ";")
	if numberOr(top, 1) <= 1 && numberOr(bottom, 0) == 0 {
		m.scroll = ""
		return
	}
	m.scroll = params
}

func (m *termModel) setModes(params string, set bool) bool {
	for p := range strings.SplitSeq(params, ";") {
		n, ok := number(p)
		if !ok {
			return false
		}
		if n == 1049 {
			m.altScreen(set)
			continue
		}
		if set == (n == 25) {
			delete(m.modes, n)
		} else {
			m.modes[n] = set
		}
	}
	return true
}

// altScreen is mode 1049: DECSC and a switch to the alternate screen,
// or a switch to the main screen and DECRC.
func (m *termModel) altScreen(set bool) {
	if set {
		if !m.alt {
			m.saveCursor()
			m.alt = true
		}
		return
	}
	m.alt = false
	m.restoreCursor()
}

func (m *termModel) kittyPush(params string) {
	s := m.screen()
	m.kitty[s] = append(m.kitty[s], numberOr(params, 0))
	if len(m.kitty[s]) > kittyStackSize {
		m.kitty[s] = m.kitty[s][1:]
	}
}

// kittyPop pops n entries, one without a number. A pop past the bottom
// empties the stack, the user's entry with it.
func (m *termModel) kittyPop(params string) bool {
	n := numberOr(params, 1)
	if n < 0 {
		return false
	}
	s := m.screen()
	m.kitty[s] = m.kitty[s][:max(0, len(m.kitty[s])-n)]
	return true
}

// keyResource is XTMODKEYS: `CSI > Pp ; Pv m` sets resource Pp, and
// `CSI > Pp m` resets it.
func (m *termModel) keyResource(params string) bool {
	res, value, hasValue := strings.Cut(params, ";")
	n, ok := number(res)
	if !ok {
		return false
	}
	if hasValue {
		m.keyResources[n] = value
	} else {
		delete(m.keyResources, n)
	}
	return true
}

// softReset is DECSTR as the VT510 manual lists it, for the states the
// model keeps: the cursor shown, the style normal, the whole screen as
// scroll region, ASCII in every character set with G0 invoked, the
// numeric keypad, and the saved cursor at its defaults. It doesn't
// change the kitty keyboard stacks, which the protocol's documentation
// doesn't tie to DECSTR. Ghostty leaves them too, and kitty clears both
// (I345).
func (m *termModel) softReset() {
	delete(m.modes, 25)
	m.sgr = sgrState{}
	m.scroll = ""
	m.g0, m.g1, m.gl = 'B', 'B', 0
	m.keypadApp = false
	m.saved[m.screen()] = savedCursor{g0: 'B', g1: 'B'}
}

func (m *termModel) osc(t token) {
	n, payload := readOSC(string(t.raw[2:]))
	if t.cut || !n.plain {
		m.unknownToken(t.raw)
		return
	}
	known := true
	switch n.value {
	case 9:
		known = payload == "4" || strings.HasPrefix(payload, "4;")
		if known {
			m.progressReport(payload)
		}
	case 10, 11, 12:
		known = payload == "?"
	case 7501:
		if payload != "?" {
			m.statusReport(payload)
		}
	default:
		known = false
	}
	if !known {
		m.unknownToken(t.raw)
	}
}

// progressReport is `OSC 9 ; 4 ; state ; percent`. State 0, or none,
// removes the indicator.
func (m *termModel) progressReport(payload string) {
	fields := strings.Split(payload, ";")
	m.progress = 0
	if len(fields) > 1 {
		m.progress = numberOr(fields[1], 0)
	}
}

// statusReport opens the report of its id, or with `state=clear`
// closes it. A clear without an id closes every report.
func (m *termModel) statusReport(payload string) {
	pairs := map[string]string{}
	for p := range strings.SplitSeq(payload, ";") {
		k, v, _ := strings.Cut(p, "=")
		pairs[k] = v
	}
	id, hasID := pairs["id"]
	switch {
	case pairs["state"] != "clear":
		m.status[id] = true
	case hasID:
		delete(m.status, id)
	default:
		clear(m.status)
	}
}

// apply applies the parameters of one SGR.
func (s *sgrState) apply(params string) {
	groups := strings.Split(params, ";")
	for i := 0; i < len(groups); i++ {
		subs := strings.Split(groups[i], ":")
		code := numberOr(subs[0], 0)
		switch code {
		case 38, 48, 58:
			var color string
			color, i = colorValue(groups, i, len(subs) > 1)
			s.setColor(code, color)
		case 4:
			s.underline = 1
			if len(subs) > 1 {
				s.underline = numberOr(subs[1], 0)
			}
		default:
			s.code(code)
		}
	}
}

// colorValue reads the color of a 38, 48 or 58 at groups[i]: from its
// own `:` sub-parameters, or from the `;` groups after it, `5 ; n` or
// `2 ; r ; g ; b`. It returns the color and the index of its last
// group.
func colorValue(groups []string, i int, colon bool) (string, int) {
	if colon {
		return groups[i], i
	}
	next := func(k int) string {
		if k < len(groups) {
			return groups[k]
		}
		return "?"
	}
	switch next(i + 1) {
	case "5":
		return "5;" + next(i+2), i + 2
	case "2":
		return "2;" + next(i+2) + ";" + next(i+3) + ";" + next(i+4), i + 4
	}
	return "?" + next(i+1), i + 1
}

func (s *sgrState) setColor(code int, color string) {
	switch code {
	case 38:
		s.fg = color
	case 48:
		s.bg = color
	default:
		s.ul = color
	}
}

// code applies one SGR code other than 4, 38, 48 and 58. A code the
// model doesn't name sets other.
func (s *sgrState) code(c int) {
	if c == 0 {
		*s = sgrState{}
		return
	}
	if set, ok := sgrCodes[c]; ok {
		set(s)
		return
	}
	s.other = true
}

// sgrCodes are the codes SGR sets other than 0, 4, 38, 48 and 58: the
// attributes, their partial resets, and the colors of 16.
var sgrCodes = func() map[int]func(*sgrState) {
	m := maps.Clone(sgrAttributes)
	ranges := []struct {
		from, to int
		set      func(s *sgrState, c int)
	}{
		{30, 37, func(s *sgrState, c int) { s.fg = strconv.Itoa(c) }},
		{90, 97, func(s *sgrState, c int) { s.fg = strconv.Itoa(c) }},
		{40, 47, func(s *sgrState, c int) { s.bg = strconv.Itoa(c) }},
		{100, 107, func(s *sgrState, c int) { s.bg = strconv.Itoa(c) }},
		{11, 19, func(s *sgrState, c int) { s.font = c }},
		{60, 64, func(s *sgrState, c int) { s.ideogram = c }},
	}
	for _, r := range ranges {
		for c := r.from; c <= r.to; c++ {
			m[c] = func(s *sgrState) { r.set(s, c) }
		}
	}
	return m
}()

// sgrAttributes are the attributes SGR sets one code each, and their
// partial resets.
var sgrAttributes = map[int]func(*sgrState){
	1:  func(s *sgrState) { s.bold = true },
	2:  func(s *sgrState) { s.faint = true },
	3:  func(s *sgrState) { s.italic = true },
	5:  func(s *sgrState) { s.blink = 5 },
	6:  func(s *sgrState) { s.blink = 6 },
	7:  func(s *sgrState) { s.inverse = true },
	8:  func(s *sgrState) { s.hidden = true },
	9:  func(s *sgrState) { s.strike = true },
	10: func(s *sgrState) { s.font = 0 },
	20: func(s *sgrState) { s.fraktur = true },
	21: func(s *sgrState) { s.underline = 2 },
	22: func(s *sgrState) { s.bold, s.faint = false, false },
	23: func(s *sgrState) { s.italic, s.fraktur = false, false },
	24: func(s *sgrState) { s.underline = 0 },
	25: func(s *sgrState) { s.blink = 0 },
	26: func(s *sgrState) { s.proportional = true },
	27: func(s *sgrState) { s.inverse = false },
	28: func(s *sgrState) { s.hidden = false },
	29: func(s *sgrState) { s.strike = false },
	39: func(s *sgrState) { s.fg = "" },
	49: func(s *sgrState) { s.bg = "" },
	50: func(s *sgrState) { s.proportional = false },
	51: func(s *sgrState) { s.framed = 51 },
	52: func(s *sgrState) { s.framed = 52 },
	53: func(s *sgrState) { s.overline = true },
	54: func(s *sgrState) { s.framed = 0 },
	55: func(s *sgrState) { s.overline = false },
	59: func(s *sgrState) { s.ul = "" },
	65: func(s *sgrState) { s.ideogram = 0 },
	73: func(s *sgrState) { s.script = 73 },
	74: func(s *sgrState) { s.script = 74 },
	75: func(s *sgrState) { s.script = 0 },
}
