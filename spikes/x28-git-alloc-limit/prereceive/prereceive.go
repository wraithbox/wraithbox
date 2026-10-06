// Package prereceive is the X26 spike's pre-receive check: a program
// that wb-hostd owns, run by `git receive-pack` from a hooks directory
// that wb-hostd owns (core.hooksPath), never one the repository
// supplies. It runs while the pushed objects are still in the
// quarantine directory. Exiting non-zero makes git delete the quarantine
// and refuse every ref, so nothing of a refused push reaches
// landing.git.
//
// It repeats the ref checks git only makes in update(), after the
// quarantine has already moved into the repository, and checks the
// inflated size of every quarantined object.
//
// Throwaway code for X26-pre-receive-check. Not held to the project gates.
package prereceive

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Config comes from the environment wb-hostd sets on receive-pack, which
// git passes on to the hook. The guest controls none of it.
type Config struct {
	Git           string
	GitDir        string // absolute path of landing.git
	Quarantine    string // GIT_QUARANTINE_PATH
	Prefix        string // refs/heads/wb/<session-id>/
	MaxObjectSize uint64
	MaxTotal      uint64
	PathMax       int // longest path the file system takes, including the NUL (darwin 1024, linux 4096)
	NameMax       int // longest path component (255)
	CheckStray    bool
	Log           io.Writer
}

// Command is one line of the hook's stdin.
type Command struct{ Old, New, Ref string }

// Refusal names the rule that refused the push.
type Refusal struct{ Rule, Ref, Detail string }

func (r *Refusal) Error() string { return fmt.Sprintf("%s: %s %s", r.Rule, r.Ref, r.Detail) }

const maxCommands = 64

var zero = strings.Repeat("0", 40)

// ReadCommands reads the hook's stdin: "<old> <new> <ref>" lines.
func ReadCommands(r io.Reader) ([]Command, error) {
	var cmds []Command
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 4096)
	for sc.Scan() {
		f := strings.Split(sc.Text(), " ")
		if len(f) != 3 || len(f[0]) != 40 || len(f[1]) != 40 {
			return nil, errors.New("bad hook input line")
		}
		if len(cmds) == maxCommands {
			return nil, errors.New("too many commands")
		}
		cmds = append(cmds, Command{f[0], f[1], f[2]})
	}
	return cmds, sc.Err()
}

func (c *Config) logf(format string, a ...any) {
	fmt.Fprintf(c.Log, format+"\n", a...)
}

func (c *Config) git(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(c.Git, args...)
	cmd.Dir = c.GitDir
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// Check runs every check and returns the first refusal. It logs each
// decision with its rule.
func (c *Config) Check(cmds []Command) error {
	if err := c.checkRefs(cmds); err != nil {
		return err
	}
	if err := c.checkSizes(); err != nil {
		return err
	}
	if c.CheckStray {
		if err := c.checkStray(cmds); err != nil {
			return err
		}
	}
	return nil
}

// existing returns the refs of landing.git, loose and packed, by name.
func (c *Config) existing() (map[string]string, error) {
	out, err := c.git(nil, "for-each-ref", "--format=%(objectname) %(refname)").Output()
	if err != nil {
		return nil, fmt.Errorf("for-each-ref: %w", err)
	}
	refs := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l == "" {
			continue
		}
		oid, name, _ := strings.Cut(l, " ")
		refs[name] = oid
	}
	return refs, nil
}

func (c *Config) checkRefs(cmds []Command) error {
	refs, err := c.existing()
	if err != nil {
		return err
	}
	// Names the push will leave behind, and which of them are new.
	after := map[string]bool{}
	for r := range refs {
		after[r] = true
	}
	for _, cm := range cmds {
		after[cm.Ref] = true
	}
	for _, cm := range cmds {
		// Ref name: the filter in front of receive-pack already ran this,
		// so this is the second layer.
		if !strings.HasPrefix(cm.Ref, c.Prefix) || len(cm.Ref) == len(c.Prefix) {
			return c.refuse("ref-prefix", cm.Ref, "outside "+c.Prefix)
		}
		// Path length: the files backend writes <gitdir>/<ref>.lock and
		// renames it. A name the filter allows can still be too long for
		// the file system once the state directory is in front of it.
		path := filepath.Join(c.GitDir, cm.Ref) + ".lock"
		if len(path)+1 > c.PathMax {
			return c.refuse("ref-path-length", cm.Ref, fmt.Sprintf("path %d bytes, limit %d", len(path), c.PathMax-1))
		}
		for _, comp := range strings.Split(cm.Ref, "/") {
			if len(comp)+len(".lock") > c.NameMax {
				return c.refuse("ref-component-length", cm.Ref, fmt.Sprintf("component %d bytes, limit %d", len(comp), c.NameMax-len(".lock")))
			}
		}
		// Old object id: git compares it under the ref lock in update().
		// wb-hostd holds the landing lock for the whole push, so the value
		// read here is the value update() sees.
		cur, exists := refs[cm.Ref]
		switch {
		case cm.Old == zero && exists:
			return c.refuse("ref-old-oid", cm.Ref, "push creates a ref that exists")
		case cm.Old != zero && !exists:
			return c.refuse("ref-old-oid", cm.Ref, "push updates a ref that does not exist")
		case cm.Old != zero && cur != cm.Old:
			return c.refuse("ref-old-oid", cm.Ref, "old object id is stale")
		}
		// Directory/file conflict: no other ref may be a directory
		// prefix of this one, or have this one as a directory prefix.
		for other := range after {
			if other == cm.Ref {
				continue
			}
			if strings.HasPrefix(other, cm.Ref+"/") || strings.HasPrefix(cm.Ref, other+"/") {
				return c.refuse("ref-dir-file", cm.Ref, "conflicts with "+other)
			}
		}
		// Case clash: on a case-insensitive file system (APFS default,
		// NTFS) two names that differ only in case are the same file or
		// directory. Compare every directory prefix too.
		for other := range after {
			if other == cm.Ref {
				continue
			}
			if p, ok := caseClash(cm.Ref, other); ok {
				return c.refuse("ref-case", cm.Ref, "differs only in case from "+p)
			}
		}
		c.logf("decision=allow rule=refs ref=%s old=%s new=%s", cm.Ref, cm.Old, cm.New)
	}
	return nil
}

// caseClash reports whether a and b share a path prefix that is equal
// when case-folded but not byte for byte.
func caseClash(a, b string) (string, bool) {
	ac, bc := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(ac) && i < len(bc); i++ {
		if ac[i] == bc[i] {
			continue
		}
		if strings.EqualFold(fold(ac[i]), fold(bc[i])) {
			return strings.Join(bc[:i+1], "/"), true
		}
		return "", false
	}
	return "", false
}

func fold(s string) string { return strings.Map(unicode.ToLower, s) }

func (c *Config) refuse(rule, ref, detail string) error {
	c.logf("decision=deny rule=%s ref=%s detail=%q", rule, ref, detail)
	return &Refusal{rule, ref, detail}
}

// checkSizes lists every object in the quarantine, and only there, with
// its inflated size. For a delta git reads the size from the delta
// header without applying it.
func (c *Config) checkSizes() error {
	cmd := c.git([]string{
		"GIT_OBJECT_DIRECTORY=" + c.Quarantine,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=",
	}, "cat-file", "--batch-all-objects", "--unordered", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var total uint64
	var n int
	sc := bufio.NewScanner(out)
	var refusal error
	for sc.Scan() {
		f := bytes.Fields(sc.Bytes())
		if len(f) != 3 {
			refusal = fmt.Errorf("cat-file: bad line %q", sc.Text())
			break
		}
		size, err := strconv.ParseUint(string(f[2]), 10, 64)
		if err != nil {
			refusal = fmt.Errorf("cat-file: bad size %q", f[2])
			break
		}
		n++
		total += size
		if size > c.MaxObjectSize {
			refusal = c.refuse("object-size", string(f[0]), fmt.Sprintf("%s of %d bytes, limit %d", f[1], size, c.MaxObjectSize))
			break
		}
		if total > c.MaxTotal {
			refusal = c.refuse("total-size", string(f[0]), fmt.Sprintf("objects total %d bytes, limit %d", total, c.MaxTotal))
			break
		}
	}
	if refusal != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return refusal
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("cat-file: %w", err)
	}
	c.logf("decision=allow rule=object-size objects=%d total=%d", n, total)
	return nil
}

// checkStray refuses objects in the quarantine that no pushed ref
// reaches. Git keeps those after a successful push.
func (c *Config) checkStray(cmds []Command) error {
	q, err := c.git([]string{
		"GIT_OBJECT_DIRECTORY=" + c.Quarantine,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=",
	}, "cat-file", "--batch-all-objects", "--unordered", "--batch-check=%(objectname)").Output()
	if err != nil {
		return fmt.Errorf("cat-file: %w", err)
	}
	inQ := map[string]bool{}
	for _, l := range strings.Fields(string(q)) {
		inQ[l] = true
	}
	args := []string{"rev-list", "--objects", "--no-object-names"}
	for _, cm := range cmds {
		args = append(args, cm.New)
	}
	args = append(args, "--not", "--all")
	out, err := c.git(nil, args...).Output()
	if err != nil {
		return fmt.Errorf("rev-list: %w", err)
	}
	for _, l := range strings.Fields(string(out)) {
		delete(inQ, l)
	}
	if len(inQ) > 0 {
		for id := range inQ {
			return c.refuse("stray-objects", id, fmt.Sprintf("%d quarantined objects no pushed ref reaches", len(inQ)))
		}
	}
	c.logf("decision=allow rule=stray-objects")
	return nil
}
