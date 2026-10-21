package relay

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termfilter"
)

// otherPhrases says what the filter did for each rule of a sequence it
// changed rather than dropped, singular and plural.
var otherPhrases = map[termfilter.Rule][2]string{
	termfilter.RuleOSCNotify:     {"notification became a bell", "notifications became a bell"},
	termfilter.RuleOSCStatusText: {"status report lost its text", "status reports lost their text"},
}

// dropNouns names what each rule of a drop counts, singular and plural,
// for the summary's `terminal:` line (S05-01-agent-commands, "Summary on
// exit").
var dropNouns = map[termfilter.Rule][2]string{
	termfilter.RuleC1:           {"C1 control", "C1 controls"},
	termfilter.RuleC0:           {"control character", "control characters"},
	termfilter.RuleESC:          {"escape sequence", "escape sequences"},
	termfilter.RuleCSI:          {"control sequence", "control sequences"},
	termfilter.RuleCSIWindow:    {"window operation", "window operations"},
	termfilter.RuleOSCTitle:     {"title change", "title changes"},
	termfilter.RuleOSCLink:      {"link sequence", "link sequences"},
	termfilter.RuleOSCClipboard: {"clipboard write", "clipboard writes"},
	termfilter.RuleOSCFile:      {"file or directory report", "file or directory reports"},
	termfilter.RuleOSC:          {"OSC sequence", "OSC sequences"},
	termfilter.RuleDCS:          {"DCS sequence", "DCS sequences"},
	termfilter.RuleAPC:          {"APC sequence", "APC sequences"},
	termfilter.RulePMOrSOS:      {"PM or SOS sequence", "PM or SOS sequences"},
}

// summaryLines returns the summary's `terminal:` and `links:` lines for
// the filters' counts and links, or none when every count is zero and
// no link was kept. Link text and URLs are guest text, stripped of the
// escaped set (S16-03-around-the-stream, "Guest text elsewhere").
func summaryLines(filters ...*termfilter.Filter) []string {
	var lines []string
	if t := terminalLine(filters); t != "" {
		lines = append(lines, "  terminal: "+t)
	}
	return append(lines, linkLines(filters)...)
}

func terminalLine(filters []*termfilter.Filter) string {
	counts := map[termfilter.Rule]uint64{}
	for _, f := range filters {
		for r, n := range f.Counts() {
			counts[r] += n
		}
	}
	var dropped, other []string
	for _, r := range termfilter.Rules() {
		n := counts[r]
		if n == 0 {
			continue
		}
		if nouns, ok := dropNouns[r]; ok {
			dropped = append(dropped, plural(n, nouns[0], nouns[1]))
		} else {
			other = append(other, plural(n, otherPhrases[r][0], otherPhrases[r][1]))
		}
	}
	if len(dropped) > 0 {
		dropped[0] = "dropped " + dropped[0]
	}
	return strings.Join(append(dropped, other...), ", ")
}

func plural(n uint64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// shownLink is a link as the summary shows it.
type shownLink struct {
	text, url, mark string
}

// maxShownText is the most code points of a link's text the summary
// shows, so a long text doesn't push the URLs off the screen.
const maxShownText = 40

func linkLines(filters []*termfilter.Filter) []string {
	links, more := collectLinks(filters)
	if len(links) == 0 && more == 0 {
		return nil
	}
	width := 0
	for _, l := range links {
		width = max(width, utf8.RuneCountInString(l.text))
	}
	lines := make([]string, 0, len(links)+1)
	for i, l := range links {
		lead := "            "
		if i == 0 {
			lead = "  links:    "
		}
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(l.text))
		line := lead + l.text + pad + "  " + l.url
		if l.mark != "" {
			line += "  (" + l.mark + ")"
		}
		lines = append(lines, line)
	}
	if more > 0 {
		lead := "            "
		if len(links) == 0 {
			lead = "  links:    "
		}
		lines = append(lines, fmt.Sprintf("%sand %d more", lead, more))
	}
	return lines
}

// collectLinks returns the links of the filters without repeats, and
// the count of those past the filters' caps.
func collectLinks(filters []*termfilter.Filter) ([]shownLink, uint64) {
	var links []shownLink
	seen := map[shownLink]bool{}
	var more uint64
	for _, f := range filters {
		for _, n := range f.LinkOverflow() {
			more += n
		}
		for _, l := range f.Links() {
			s := shownLink{text: cutText(guesttext.Strip(l.Text)), url: guesttext.Strip(l.URL), mark: strings.ReplaceAll(string(l.Mark), "-", " ")}
			if l.URLCut {
				s.url += "…"
			}
			if !seen[s] {
				seen[s] = true
				links = append(links, s)
			}
		}
	}
	return links, more
}

func cutText(s string) string {
	if utf8.RuneCountInString(s) <= maxShownText {
		return s
	}
	r := []rune(s)
	return string(r[:maxShownText-1]) + "…"
}
