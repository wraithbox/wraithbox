// Throwaway (I76): the checksum-database clock for Go's minimum age.
//
// With -go-clock sumdb, a Go module version's age comes from its record
// number in sum.golang.org, not from the .info Time (the commit time). The
// calibration point is the record number at now - min-age: the minimum
// record number of up to 20 index.golang.org entries first seen in the ten
// minutes before that time, minus one minute. A version whose record number
// is at or below the point is old enough. Anything else is young, and so is
// a version the gate can't look up. Without a calibration point every Go
// download and listing is refused (fail closed).
//
// The sumdb lookups the go command makes go through this proxy too
// (GOPROXY=http://<addr>/goproxy, which the go command asks for
// /sumdb/sum.golang.org/supported), so sum.golang.org stays reachable with
// GOSUMDB on and the go command still checks every signed tree it gets.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	goClock      = flag.String("go-clock", "info", "info|sumdb: Go publish-time source")
	indexURL     = flag.String("index-url", "https://index.golang.org", "module index base URL")
	sumdbURL     = flag.String("sumdb-url", "https://sum.golang.org", "checksum database base URL")
	calRefresh   = flag.Duration("cal-refresh", time.Hour, "recalibrate when the point is older than this")
	listParallel = flag.Int("list-parallel", 16, "sumdb lookups at once when filtering @v/list")
	listLazy     = flag.Bool("list-lazy", false, "filter @v/list from the highest version down, stop at the first old one")
	indexBreak   = flag.Bool("index-break-after-first", false, "test: make the index unreachable after the first calibration")
)

type calibration struct {
	at       time.Time // when it was computed
	cutoff   time.Time // at - minAge
	point    int64     // record number at cutoff
	treeSize int64     // tree size at `at`
}

var (
	calMu   sync.Mutex
	cal     *calibration
	calErrs atomic.Int64

	sumMu    sync.Mutex
	sumCache = map[string]int64{}

	sumLookups atomic.Int64
	sumNew     atomic.Int64 // lookups that returned a record past the tree size at calibration
)

func httpGet(u string) ([]byte, int, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "wraithbox-x21-spike (I76)")
	resp, err := upstream.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return b, resp.StatusCode, fmt.Errorf("GET %s: %d %s", u, resp.StatusCode, strings.TrimSpace(string(b[:min(len(b), 120)])))
	}
	return b, 200, err
}

func sumdbTreeSize() (int64, error) {
	b, _, err := httpGet(*sumdbURL + "/latest")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) < 2 {
		return 0, errors.New("short /latest")
	}
	return strconv.ParseInt(lines[1], 10, 64)
}

// sumdbRecord looks up path@version (both already escaped) and returns its
// record number. A lookup of a version the checksum database hasn't seen
// adds it, with a record number past the current tree size.
func sumdbRecord(escMod, escVer string) (int64, error) {
	k := escMod + "@" + escVer
	sumMu.Lock()
	r, ok := sumCache[k]
	sumMu.Unlock()
	if ok {
		return r, nil
	}
	b, _, err := httpGet(*sumdbURL + "/lookup/" + k)
	sumLookups.Add(1)
	if err != nil {
		return 0, err
	}
	first, _, _ := strings.Cut(string(b), "\n")
	r, err = strconv.ParseInt(first, 10, 64)
	if err != nil || r < 0 {
		return 0, fmt.Errorf("bad record number %q", first)
	}
	if c := currentCal(); c != nil && r >= c.treeSize {
		sumNew.Add(1)
	}
	sumMu.Lock()
	sumCache[k] = r // a record number never changes
	sumMu.Unlock()
	return r, nil
}

type indexEntry struct {
	Path, Version string
	Timestamp     time.Time
}

func calibrate(at time.Time) (*calibration, error) {
	since := at.Add(-*minAge - 10*time.Minute).UTC().Format(time.RFC3339Nano)
	b, _, err := httpGet(*indexURL + "/index?limit=400&since=" + since)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	cutoff := at.Add(-*minAge)
	var win []indexEntry
	for _, l := range strings.Split(string(b), "\n") {
		var e indexEntry
		if json.Unmarshal([]byte(l), &e) == nil && !e.Timestamp.After(cutoff.Add(-time.Minute)) {
			win = append(win, e)
		}
	}
	if len(win) == 0 {
		return nil, errors.New("index: no entries before the cutoff")
	}
	if len(win) > 20 {
		win = win[len(win)-20:]
	}
	ts, err := sumdbTreeSize()
	if err != nil {
		return nil, fmt.Errorf("sumdb latest: %w", err)
	}
	point := int64(-1)
	for _, e := range win {
		r, err := sumdbRecord(goEscape(e.Path), goEscape(e.Version))
		if err == nil && (point < 0 || r < point) {
			point = r
		}
	}
	if point < 0 {
		return nil, errors.New("no record number in the calibration window")
	}
	return &calibration{at: at, cutoff: cutoff, point: point, treeSize: ts}, nil
}

func currentCal() *calibration {
	calMu.Lock()
	defer calMu.Unlock()
	return cal
}

// ensureCal returns a calibration point, recomputing it when it is older
// than -cal-refresh. A stale point is kept when recomputing fails: it is
// lower than a fresh one, so it only refuses more. With no point at all the
// caller refuses.
func ensureCal() (*calibration, error) {
	calMu.Lock()
	c := cal
	calMu.Unlock()
	if c != nil && time.Since(c.at) < *calRefresh {
		return c, nil
	}
	n, err := calibrate(now.Add(time.Since(startWall)))
	if err != nil {
		calErrs.Add(1)
		emit(event{Host: "index.golang.org", Kind: "calibration", Decision: "error", Rule: err.Error()})
		if c != nil {
			return c, nil
		}
		return nil, err
	}
	calMu.Lock()
	cal = n
	calMu.Unlock()
	if *indexBreak {
		*indexURL = "http://127.0.0.1:9"
	}
	emit(event{Host: "index.golang.org", Kind: "calibration", Decision: "ok",
		Rule: fmt.Sprintf("point %d at %s, tree %d", n.point, n.cutoff.Format(time.RFC3339), n.treeSize)})
	return n, nil
}

var startWall = time.Now()

// goSumdbTime maps a record number to a synthetic publish time: at or before
// the cutoff when the record is at or below the point, after it otherwise,
// interpolated on the rate of records between the point and the tree size.
func goSumdbTime(escMod, escVer string) (time.Time, int64, error) {
	c, err := ensureCal()
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("go-clock-uncalibrated: %w", err)
	}
	r, err := sumdbRecord(escMod, escVer)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("sumdb-lookup: %w", err)
	}
	rate := float64(c.treeSize-c.point) / c.at.Sub(c.cutoff).Seconds() // records per second
	if rate <= 0 {
		rate = 1
	}
	d := time.Duration(float64(r-c.point) / rate * float64(time.Second))
	if r > c.point && d <= 0 {
		d = time.Nanosecond
	}
	return c.cutoff.Add(d), r, nil
}

func goEscape(p string) string {
	var b strings.Builder
	for _, c := range p {
		if c >= 'A' && c <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(c - 'A' + 'a')
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// goAgeTime is the Go publish time under the chosen clock.
func goAgeTime(escMod, escVer string) (time.Time, error) {
	if *goClock == "sumdb" {
		t, _, err := goSumdbTime(escMod, escVer)
		return t, err
	}
	return goInfoTime(escMod, escVer)
}

// filterList keeps the listed versions that are old enough. Full: one lookup
// per listed version. Lazy: highest version first, stop at the first old
// one, and pass everything below it unchecked (the download check still
// applies to them).
func filterList(escMod string, lines []string) (keep []string, lookups int, err error) {
	if *listLazy {
		sorted := append([]string(nil), lines...)
		sort.Slice(sorted, func(i, j int) bool { return semverCmp(sorted[j], sorted[i]) < 0 })
		for i, v := range sorted {
			t, err := goAgeTime(escMod, goEscape(v))
			lookups++
			if err != nil && strings.HasPrefix(err.Error(), "go-clock-uncalibrated") {
				return nil, lookups, err
			}
			if err == nil && !tooYoung(t) {
				keep = append(keep, sorted[i:]...)
				break
			}
		}
		sort.Strings(keep)
		return keep, lookups, nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, *listParallel)
	for _, v := range lines {
		wg.Add(1)
		sem <- struct{}{}
		go func(v string) {
			defer wg.Done()
			defer func() { <-sem }()
			t, err := goAgeTime(escMod, goEscape(v))
			mu.Lock()
			defer mu.Unlock()
			lookups++
			if err != nil && strings.HasPrefix(err.Error(), "go-clock-uncalibrated") && firstErr == nil {
				firstErr = err
			}
			if err == nil && !tooYoung(t) {
				keep = append(keep, v)
			}
		}(v)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, lookups, firstErr
	}
	sort.Strings(keep)
	return keep, lookups, nil
}

// sumdbPassThrough serves <GOPROXY>/sumdb/sum.golang.org/... from
// sum.golang.org, and logs the record number of each lookup the go command
// makes.
func sumdbPassThrough(w http.ResponseWriter, r *http.Request, rest string) {
	e := event{Host: "sum.golang.org", Path: rest, Kind: "sumdb"}
	if rest == "/supported" {
		e.Decision = "allow"
		emit(e)
		w.WriteHeader(200)
		return
	}
	start := time.Now()
	b, code, err := httpGet(*sumdbURL + rest)
	e.Ms = float64(time.Since(start).Microseconds()) / 1000
	e.Status = code
	if err != nil && code == 0 {
		e.Decision, e.Rule = "error", err.Error()
		emit(e)
		http.Error(w, err.Error(), 502)
		return
	}
	if strings.HasPrefix(rest, "/lookup/") && code == 200 {
		first, _, _ := strings.Cut(string(b), "\n")
		e.Version = first // record number
	}
	e.Decision = "allow"
	emit(e)
	w.WriteHeader(code)
	w.Write(b)
}

func clockStats() string {
	c := currentCal()
	s := fmt.Sprintf("sumdb lookups %d, records past the calibration tree size %d, calibration errors %d", sumLookups.Load(), sumNew.Load(), calErrs.Load())
	if c != nil {
		s += fmt.Sprintf(", point %d tree %d", c.point, c.treeSize)
	}
	return s
}

// semverCmp compares Go versions vMAJOR.MINOR.PATCH[-pre][+build] well
// enough for ordering a list: numeric parts, then a release above its
// prereleases, then prereleases as strings. (main.go's semverLess ignores
// the "v" and the prerelease, which ordered terraform's list wrongly.)
func semverCmp(a, b string) int {
	core := func(v string) ([3]int, string) {
		v = strings.TrimPrefix(strings.SplitN(v, "+", 2)[0], "v")
		c, pre, _ := strings.Cut(v, "-")
		var n [3]int
		for i, p := range strings.SplitN(c, ".", 3) {
			n[i], _ = strconv.Atoi(p)
		}
		return n, pre
	}
	na, pa := core(a)
	nb, pb := core(b)
	for i := range 3 {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	case pa < pb:
		return -1
	}
	return 1
}
