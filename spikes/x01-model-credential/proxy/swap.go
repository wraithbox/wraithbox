package main

// x01-proxy swap: move a browser login out of Claude Code's own store
// into the proxy's Keychain item, and leave placeholders behind. With
// -restore it puts the real login back, so /logout can revoke it.
// Prints field names, lengths and times, never a token.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// loginKey is the field of Claude Code's credential JSON that holds the
// browser login. The run checks it: swap fails and lists the fields if
// it's missing.
const loginKey = "claudeAiOauth"

func swapMain(args []string) error {
	fs := flag.NewFlagSet("swap", flag.ExitOnError)
	from := fs.String("from", "keychain:Claude Code-credentials", "Claude Code's store: keychain:<service> or file:<path>")
	store := fs.String("store", "wraithbox-oauth", "Keychain service of the proxy's item")
	accessPH := fs.String("access-placeholder", "", "placeholder access token")
	refreshPH := fs.String("refresh-placeholder", "", "placeholder refresh token")
	expire := fs.Bool("expire", false, "set expiresAt an hour in the past, to force a refresh")
	restore := fs.Bool("restore", false, "put the real login from -store back into -from")
	show := fs.Bool("show", false, "only print the fields of -from")
	expireOnly := fs.Bool("expire-only", false, "only set expiresAt an hour in the past")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := readStore(*from)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%s is not JSON (%d bytes)", *from, len(raw))
	}
	var login map[string]any
	if err := json.Unmarshal(top[loginKey], &login); err != nil || login == nil {
		return fmt.Errorf("%s has no %s object; its fields: %s", *from, loginKey, keysOf(top))
	}
	fmt.Printf("%s: %s fields: %s\n", *from, loginKey, describeLogin(login))

	switch {
	case *show:
		return nil
	case *expireOnly:
		login["expiresAt"] = float64(time.Now().Add(-time.Hour).UnixMilli())
	case *restore:
		out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", *store, "-w").Output()
		if err != nil {
			return fmt.Errorf("read %s: %w", *store, err)
		}
		var l storedLogin
		if err := json.Unmarshal(bytes.TrimSpace(out), &l); err != nil || l.Access == "" || l.Refresh == "" {
			return fmt.Errorf("%s does not hold {access,refresh}", *store)
		}
		login["accessToken"], login["refreshToken"] = l.Access, l.Refresh
		// A past expiry makes Claude Code refresh before it uses the
		// token, so a stale access token doesn't matter.
		login["expiresAt"] = float64(time.Now().Add(-time.Hour).UnixMilli())
	default:
		if *accessPH == "" || *refreshPH == "" {
			return errors.New("-access-placeholder and -refresh-placeholder are required")
		}
		access, _ := login["accessToken"].(string)
		refresh, _ := login["refreshToken"].(string)
		if access == "" || refresh == "" {
			return errors.New("the login lacks accessToken or refreshToken")
		}
		if access == *accessPH {
			return errors.New("the login already holds the placeholders; run with -restore first")
		}
		b, _ := json.Marshal(storedLogin{Access: access, Refresh: refresh})
		if err := exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", *store,
			"-a", "x01", "-T", "/usr/bin/security", "-w", string(b)).Run(); err != nil {
			return fmt.Errorf("write %s: %w", *store, err)
		}
		fmt.Printf("stored the real login in Keychain item %s (access %d bytes, refresh %d bytes)\n",
			*store, len(access), len(refresh))
		login["accessToken"], login["refreshToken"] = *accessPH, *refreshPH
		if *expire {
			login["expiresAt"] = float64(time.Now().Add(-time.Hour).UnixMilli())
		}
	}
	lb, _ := json.Marshal(login)
	top[loginKey] = lb
	nb, _ := json.Marshal(top)
	if err := writeStore(*from, nb); err != nil {
		return err
	}
	fmt.Printf("wrote %s back: %s\n", *from, describeLogin(login))
	return nil
}

// patternsMain prints the real access and refresh token of -store, one
// per line, for grep -f /dev/stdin in the disk search. Never run it to
// a terminal or a file.
func patternsMain(args []string) error {
	fs := flag.NewFlagSet("patterns", flag.ExitOnError)
	store := fs.String("store", "wraithbox-oauth", "Keychain service of the proxy's item")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return errors.New("stdout is a terminal; pipe it into grep -f /dev/stdin")
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", *store, "-w").Output()
	if err != nil {
		return fmt.Errorf("read %s: %w", *store, err)
	}
	var l storedLogin
	if err := json.Unmarshal(bytes.TrimSpace(out), &l); err != nil || l.Access == "" || l.Refresh == "" {
		return fmt.Errorf("%s does not hold {access,refresh}", *store)
	}
	fmt.Printf("%s\n%s\n", l.Access, l.Refresh)
	return nil
}

func readStore(src string) ([]byte, error) {
	kind, arg, _ := strings.Cut(src, ":")
	switch kind {
	case "keychain":
		out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", arg, "-w").Output()
		if err != nil {
			return nil, fmt.Errorf("security find-generic-password -s %q: %w", arg, err)
		}
		return bytes.TrimSpace(out), nil
	case "file":
		return os.ReadFile(arg)
	}
	return nil, fmt.Errorf("bad store %q", src)
}

func writeStore(src string, b []byte) error {
	kind, arg, _ := strings.Cut(src, ":")
	switch kind {
	case "keychain":
		acct, err := keychainAccount(arg)
		if err != nil {
			return err
		}
		return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", arg, "-a", acct, "-w", string(b)).Run()
	case "file":
		return os.WriteFile(arg, b, 0o600)
	}
	return fmt.Errorf("bad store %q", src)
}

// keychainAccount reads the account of an existing generic password
// from its attributes, which hold no secret.
func keychainAccount(svc string) (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", svc).Output()
	if err != nil {
		return "", fmt.Errorf("security find-generic-password -s %q: %w", svc, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, `"acct"<blob>="`); ok {
			return strings.TrimSuffix(v, `"`), nil
		}
	}
	return "", fmt.Errorf("no account in the attributes of %q", svc)
}

func keysOf(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// describeLogin prints each field: lengths for strings, times for
// expiresAt, values for the rest (scopes, subscription type).
func describeLogin(login map[string]any) string {
	keys := make([]string, 0, len(login))
	for k := range login {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := login[k]
		switch {
		case k == "expiresAt":
			if ms, ok := v.(float64); ok {
				at := time.UnixMilli(int64(ms))
				parts = append(parts, fmt.Sprintf("expiresAt=%s (in %s)", at.Format(time.RFC3339), time.Until(at).Round(time.Second)))
				continue
			}
			parts = append(parts, fmt.Sprintf("expiresAt=<%T>", v))
		case strings.Contains(strings.ToLower(k), "token"):
			s, _ := v.(string)
			parts = append(parts, fmt.Sprintf("%s=<len %d>", k, len(s)))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}
