package datadisk

import "time"

// FormatAnswerWait is how long wb-hostd waits for the format's answer.
const FormatAnswerWait = formatAnswerWait

// SetExitWait sets how long p's sessions wait for wb-diskfmt's exit
// before the kill, and after it.
func SetExitWait(p *ProcessShim, d time.Duration) { p.exitWait = d }
