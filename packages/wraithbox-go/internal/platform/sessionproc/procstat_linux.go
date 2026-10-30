package sessionproc

import (
	"bytes"
	"errors"
	"strconv"
)

// The parsers of linux's /proc files, apart from the reads, for the
// fuzz targets. A process picks its own command name, which
// /proc/<pid>/stat holds between parentheses, so the bytes are guest
// input.

// errStat is the error of a /proc file out of form.
var errStat = errors.New("sessionproc: a /proc file out of form")

// maxProcFile bounds a /proc file the table reads.
const maxProcFile = 4 << 10

// statLine is what the table takes from /proc/<pid>/stat.
type statLine struct {
	name    string
	state   byte
	session int32
	// start is the start time in clock ticks since boot.
	start int64
}

// The fields of /proc/<pid>/stat after the command name, counted from
// the state, which is field 3 of proc(5).
const (
	fieldState   = 0
	fieldSession = 3
	fieldStart   = 19
)

// parseStat reads /proc/<pid>/stat: `pid (comm) state ppid pgrp session
// … starttime …` (proc(5)). The name ends at the last `)`, because a
// name can hold `)` and spaces itself.
func parseStat(b []byte) (statLine, error) {
	if len(b) > maxProcFile {
		return statLine{}, errStat
	}
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return statLine{}, errStat
	}
	fields := bytes.Fields(b[end+1:])
	if len(fields) <= fieldStart || len(fields[fieldState]) != 1 {
		return statLine{}, errStat
	}
	sid, err := strconv.ParseInt(string(fields[fieldSession]), 10, 32)
	if err != nil || sid < 0 {
		return statLine{}, errStat
	}
	start, err := strconv.ParseInt(string(fields[fieldStart]), 10, 64)
	if err != nil || start < 0 {
		return statLine{}, errStat
	}
	return statLine{
		name: string(b[open+1 : end]), state: fields[fieldState][0],
		session: int32(sid), start: start,
	}, nil
}

// parseUIDs reads the real and effective user IDs from the `Uid:` line
// of /proc/<pid>/status: `Uid:\treal\teffective\tsaved\tfs`.
func parseUIDs(b []byte) (ruid, euid uint32, err error) {
	if len(b) > maxProcFile {
		return 0, 0, errStat
	}
	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		rest, ok := bytes.CutPrefix(line, []byte("Uid:"))
		if !ok {
			continue
		}
		fields := bytes.Fields(rest)
		if len(fields) != 4 {
			return 0, 0, errStat
		}
		r, err1 := strconv.ParseUint(string(fields[0]), 10, 32)
		e, err2 := strconv.ParseUint(string(fields[1]), 10, 32)
		if err1 != nil || err2 != nil {
			return 0, 0, errStat
		}
		return uint32(r), uint32(e), nil
	}
	return 0, 0, errStat
}
