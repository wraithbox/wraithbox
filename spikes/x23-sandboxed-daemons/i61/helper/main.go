// Command i61 is the throwaway wb-proxyd stand-in for I61: it confines
// itself with a Seatbelt profile (no cgo for the call, as X23 found), then
// checks what wb-proxyd needs from the Keychain and the Secure Enclave
// (S15-least-privilege, "Declared access") still works, and that the rest
// is denied. It runs as the main executable of a nested helper bundle signed
// by the developer's team, with the binding and ca access groups
// (X29-dev-signing).
package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"x23/sbxapply"
)

// team is set with -ldflags -X. The real daemon reads it from its own code
// signature (S15-least-privilege, "Keychain").
var team = "?"

type probe struct {
	name string
	want bool // true: must work, false: must be denied
	run  func() error
}

func main() {
	mech := flag.String("mech", "pure", "none, pure or cgo")
	profile := flag.String("profile", "", "SBPL profile file, read before confining")
	var params sbxapply.Params
	flag.Var(&params, "D", "profile parameter key=value")
	url := flag.String("url", "https://api.anthropic.com/", "HTTPS URL, checked against system roots")
	tag := flag.String("tag", "run", "suffix for item names, so runs don't collide")
	only := flag.String("only", "", "comma-separated probe name prefixes to run")
	flag.Parse()

	runtime.GOMAXPROCS(runtime.NumCPU()) // X23 condition 2
	d, err := sbxapply.Apply(*mech, *profile, params)
	if err != nil {
		fmt.Println("apply:", err)
		os.Exit(2)
	}
	fmt.Printf("i61 team=%s mech=%s profile=%s apply=%s pid=%d\n", team, *mech, *profile, d.Round(time.Microsecond), os.Getpid())
	if *mech != "none" {
		// The self-check from X23 condition 1.
		if f, err := os.Open("/etc/passwd"); err == nil {
			f.Close()
			fmt.Println("self-check: /etc/passwd opened under the profile, exiting")
			os.Exit(3)
		}
	}

	bad := 0
	for _, p := range probes(*url, *tag) {
		if *only != "" && !hasPrefix(p.name, strings.Split(*only, ",")) {
			continue
		}
		t := time.Now()
		err := p.run()
		got := err == nil
		verdict := "as expected"
		if got != p.want {
			verdict = "UNEXPECTED"
			bad++
		}
		state := "ok"
		if !got {
			state = "denied"
		}
		line := fmt.Sprintf("%-28s %-7s %-12s %8s", p.name, state, verdict, time.Since(t).Round(time.Microsecond))
		if err != nil {
			line += "  " + err.Error()
		}
		fmt.Println(line)
	}
	fmt.Printf("unexpected: %d\n", bad)
	if bad > 0 {
		os.Exit(1)
	}
}

func hasPrefix(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func group(role string) string { return team + ".nl.wraithbox." + role }

func probes(url, tag string) []probe {
	binding := "wraithbox/binding/i61-" + tag
	ca := "wraithbox/ca/i61-vm/" + tag
	return []probe{
		{"kc binding put", true, func() error { return put(group("binding"), binding, []byte("placeholder-not-a-secret")) }},
		{"kc binding get", true, func() error { _, err := get(group("binding"), binding); return err }},
		{"kc binding del", true, func() error { return del(group("binding"), binding) }},
		{"kc ca se-new+put", true, func() error { return seNew(group("ca"), ca) }},
		{"kc ca get+restore+sign", true, func() error { return seSign(group("ca"), ca) }},
		{"kc ca del", true, func() error { return del(group("ca"), ca) }},
		{"kc other group (store)", false, func() error { _, err := get(group("store"), "a"); return err }},
		{"kc file keychains (login)", false, fileKeychains},
		{"dns lookup", true, func() error { _, err := net.LookupHost("api.anthropic.com"); return err }},
		{"tls get, system roots", true, func() error { return tlsGet(url) }},
		{"file read /etc/shells", false, func() error { return readFile("/etc/shells") }},
		{"file read home dir", false, func() error { _, err := os.ReadDir(os.Getenv("HOME")); return err }},
		{"file read login keychain", false, func() error {
			return readFile(os.Getenv("HOME") + "/Library/Keychains/login.keychain-db")
		}},
		{"file write /tmp", false, func() error { return os.WriteFile("/tmp/i61-probe", []byte("x"), 0o600) }},
		{"listen tcp loopback", false, func() error { return listen("tcp", "127.0.0.1:0") }},
		{"listen unix /tmp", false, func() error { return listen("unix", "/tmp/i61-probe.sock") }},
		{"udp to 8.8.8.8:123", false, func() error { return dialUDP("8.8.8.8:123") }},
		{"exec /bin/echo", false, func() error { return exec.Command("/bin/echo", "x").Run() }},
	}
}

// fileKeychains returns nil when the search list could be read at all,
// even when it is empty, so a sandbox denial shows up as an error.
func fileKeychains() error {
	var e C.x29err
	n := C.x29_file_kc_count(&e)
	if n < 0 {
		return cerr(&e)
	}
	fmt.Printf("  file keychains: %d generic password items visible\n", int(n))
	return nil
}

func readFile(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	return f.Close()
}

func listen(network, addr string) error {
	if network == "unix" {
		os.Remove(addr)
	}
	l, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	return l.Close()
}

func dialUDP(addr string) error {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte{0})
	return err
}

func tlsGet(url string) error {
	c := &http.Client{Timeout: 15 * time.Second}
	r, err := c.Get(url)
	if err != nil {
		return err
	}
	r.Body.Close()
	fmt.Printf("  %s: HTTP %d, %s\n", url, r.StatusCode, r.TLS.PeerCertificates[0].Subject.CommonName)
	return nil
}

func cerr(e *C.x29err) error {
	return fmt.Errorf("%s (status %d)", C.GoString(&e.msg[0]), int(e.status))
}

// cs leaks; the process is short-lived.
func cs(s string) *C.char { return C.CString(s) }

func put(g, account string, v []byte) error {
	var e C.x29err
	if C.x29_put(cs(g), cs(account), (*C.uchar)(&v[0]), C.int(len(v)), &e) != 0 {
		return cerr(&e)
	}
	return nil
}

func get(g, account string) ([]byte, error) {
	var e C.x29err
	buf := make([]byte, 4096)
	n := C.x29_get(cs(g), cs(account), (*C.uchar)(&buf[0]), C.int(len(buf)), &e)
	if n < 0 {
		return nil, cerr(&e)
	}
	return buf[:n], nil
}

func del(g, account string) error {
	var e C.x29err
	if C.x29_del(cs(g), cs(account), &e) != 0 {
		return cerr(&e)
	}
	return nil
}

// seNew makes an ephemeral Secure Enclave key and keeps its token object
// ID in the ca item, as wb-proxyd does for a VM's CA key (B23 decision 2).
func seNew(g, account string) error {
	var e C.x29err
	k := C.x29_se_new(0, cs(g), cs(account), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x29_release(k)
	buf := make([]byte, 1024)
	n := C.x29_se_oid(k, (*C.uchar)(&buf[0]), C.int(len(buf)), &e)
	if n < 0 {
		return cerr(&e)
	}
	if err := put(g, account, buf[:n]); err != nil {
		return err
	}
	return signCheck(k)
}

func seSign(g, account string) error {
	oid, err := get(g, account)
	if err != nil {
		return err
	}
	var e C.x29err
	k := C.x29_se_restore((*C.uchar)(&oid[0]), C.int(len(oid)), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x29_release(k)
	return signCheck(k)
}

func signCheck(k C.SecKeyRef) error {
	var e C.x29err
	p := make([]byte, 128)
	pn := C.x29_se_pub(k, (*C.uchar)(&p[0]), C.int(len(p)), &e)
	if pn < 0 {
		return cerr(&e)
	}
	dg := sha256.Sum256([]byte("i61"))
	sig := make([]byte, 128)
	n := C.x29_se_sign(k, (*C.uchar)(&dg[0]), 32, (*C.uchar)(&sig[0]), C.int(len(sig)), &e)
	if n < 0 {
		return cerr(&e)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), p[:pn]) //nolint:staticcheck // throwaway spike code
	if !ecdsa.VerifyASN1(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, dg[:], sig[:n]) {
		return errors.New("signature does not verify")
	}
	return nil
}
