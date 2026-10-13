// Command x9 is the throwaway prototype for X09-keychain-unsigned: a
// keychain file with access-control lists for ad-hoc signed binaries,
// and Secure Enclave keys without a keychain-access-groups entitlement.
package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// variant is set with -ldflags -X so that each copy has its own cdhash.
var variant = "a"

func cerr(e *C.x9err) error {
	return fmt.Errorf("%s (status %d)", C.GoString(&e.msg[0]), int(e.status))
}

func cstrs(ss []string) (**C.char, func()) {
	if len(ss) == 0 {
		return nil, func() {}
	}
	arr := C.malloc(C.size_t(len(ss)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	view := unsafe.Slice((**C.char)(arr), len(ss))
	for i, s := range ss {
		if s == "self" {
			s = ""
		}
		view[i] = C.CString(s)
	}
	return (**C.char)(arr), func() {
		for _, p := range view {
			C.free(unsafe.Pointer(p))
		}
		C.free(arr)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: x9 <command> ...")
		os.Exit(2)
	}
	fmt.Printf("[x9-%s pid %d] %s\n", variant, os.Getpid(), strings.Join(os.Args[1:], " "))
	if os.Getenv("X9_NOUI2") != "" {
		C.x9_noui()
	}
	start := time.Now()
	err := run(os.Args[1], os.Args[2:])
	fmt.Printf("[x9-%s] took %s\n", variant, time.Since(start).Round(time.Millisecond))
	if err != nil {
		fmt.Printf("[x9-%s] FAIL: %v\n", variant, err)
		os.Exit(1)
	}
	fmt.Printf("[x9-%s] OK\n", variant)
}

func run(cmd string, a []string) error {
	var e C.x9err
	cs := func(s string) *C.char { return C.CString(s) } // leaks; throwaway
	switch cmd {
	case "kc-create":
		if C.x9_kc_create(cs(a[0]), cs(a[1]), &e) != 0 {
			return cerr(&e)
		}
	case "unlock":
		if C.x9_kc_unlock(cs(a[0]), cs(a[1]), &e) != 0 {
			return cerr(&e)
		}
	case "lock":
		if C.x9_kc_lock(cs(a[0]), &e) != 0 {
			return cerr(&e)
		}
	case "add": // add KC SERVICE VALUE (none | app...)
		apps := a[3:]
		n := len(apps)
		if n == 1 && apps[0] == "none" {
			apps, n = nil, -1
		}
		p, free := cstrs(apps)
		defer free()
		if C.x9_item_add(cs(a[0]), cs(a[1]), cs(a[2]), p, C.int(n), &e) != 0 {
			return cerr(&e)
		}
	case "read":
		buf := make([]byte, 4096)
		if C.x9_item_read(cs(a[0]), cs(a[1]), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)), &e) != 0 {
			return cerr(&e)
		}
		fmt.Printf("value: %q\n", C.GoString((*C.char)(unsafe.Pointer(&buf[0]))))
	case "delete":
		if C.x9_item_delete(cs(a[0]), cs(a[1]), &e) != 0 {
			return cerr(&e)
		}
	case "set-access": // set-access KC SERVICE PW app...
		p, free := cstrs(a[3:])
		defer free()
		if C.x9_item_set_access_pw(cs(a[0]), cs(a[1]), cs(a[2]), p, C.int(len(a)-3), &e) != 0 {
			return cerr(&e)
		}
	case "set-access2": // set-access2 KC SERVICE PW app...
		p, free := cstrs(a[3:])
		defer free()
		if C.x9_item_set_access_pw2(cs(a[0]), cs(a[1]), cs(a[2]), p, C.int(len(a)-3), &e) != 0 {
			return cerr(&e)
		}
	case "se-create": // se-create MODE LABEL [OIDFILE]
		return seCreate(a)
	case "se-find":
		k := C.x9_se_find(cs(a[0]), &e)
		if k == 0 {
			return cerr(&e)
		}
		defer C.x9_release(k)
		return selfTest(k)
	case "se-delete":
		if C.x9_se_delete(cs(a[0]), &e) != 0 {
			return cerr(&e)
		}
	case "se-restore": // se-restore HOW OIDFILE
		how, _ := strconv.Atoi(a[0])
		oid, err := os.ReadFile(a[1])
		if err != nil {
			return err
		}
		k := C.x9_se_restore(C.int(how), (*C.uchar)(unsafe.Pointer(&oid[0])), C.int(len(oid)), &e)
		if k == 0 {
			return cerr(&e)
		}
		defer C.x9_release(k)
		return selfTest(k)
	case "se-bench": // se-bench N
		n, _ := strconv.Atoi(a[0])
		return bench(n)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

func seCreate(a []string) error {
	var e C.x9err
	mode, _ := strconv.Atoi(a[0])
	k := C.x9_se_create(C.int(mode), C.CString(a[1]), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x9_release(k)
	if err := selfTest(k); err != nil {
		return err
	}
	oid := make([]byte, 4096)
	n := C.x9_se_token_oid(k, (*C.uchar)(unsafe.Pointer(&oid[0])), C.int(len(oid)), &e)
	if n < 0 {
		fmt.Printf("token OID: %v\n", cerr(&e))
		return nil
	}
	fmt.Printf("token OID: %d bytes, starts %s\n", n, hex.EncodeToString(oid[:min(int(n), 16)]))
	if len(a) > 2 {
		return os.WriteFile(a[2], oid[:n], 0o600)
	}
	return nil
}

// seSigner is a crypto.Signer backed by a Secure Enclave key.
type seSigner struct {
	k   C.SecKeyRef
	pub *ecdsa.PublicKey
}

func newSigner(k C.SecKeyRef) (*seSigner, error) {
	var e C.x9err
	buf := make([]byte, 256)
	n := C.x9_se_pub(k, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)), &e)
	if n < 0 {
		return nil, cerr(&e)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), buf[:n])
	if err != nil {
		return nil, err
	}
	return &seSigner{k: k, pub: pub}, nil
}

func (s *seSigner) Public() crypto.PublicKey { return s.pub }

func (s *seSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 {
		return nil, errors.New("only SHA-256")
	}
	var e C.x9err
	buf := make([]byte, 128)
	n := C.x9_se_sign(s.k, (*C.uchar)(unsafe.Pointer(&digest[0])), C.int(len(digest)),
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)), &e)
	if n < 0 {
		return nil, cerr(&e)
	}
	return buf[:n], nil
}

func selfTest(k C.SecKeyRef) error {
	s, err := newSigner(k)
	if err != nil {
		return err
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(s.pub)
	sum := sha256.Sum256(pubDER)
	fmt.Printf("public key SHA-256: %s\n", hex.EncodeToString(sum[:8]))
	d := sha256.Sum256([]byte("x09"))
	sig, err := s.Sign(nil, d[:], crypto.SHA256)
	if err != nil {
		return err
	}
	if !ecdsa.VerifyASN1(s.pub, d[:], sig) {
		return errors.New("signature does not verify")
	}
	ca, err := makeCA(s)
	if err != nil {
		return err
	}
	fmt.Printf("self-signed CA %q verified\n", ca.Subject.CommonName)
	return nil
}

func makeCA(s crypto.Signer) (*x509.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "X09 spike CA " + variant},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, s.Public(), s)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return ca, ca.CheckSignatureFrom(ca)
}

func leaf(ca *x509.Certificate, s crypto.Signer, i int) error {
	lk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("host%d.example.com", i)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(int64(i + 2)),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	_, err = x509.CreateCertificate(rand.Reader, tmpl, ca, &lk.PublicKey, s)
	return err
}

func timeLeaves(label string, s crypto.Signer, n int) error {
	ca, err := makeCA(s)
	if err != nil {
		return err
	}
	ds := make([]time.Duration, n)
	for i := range n {
		t := time.Now()
		if err := leaf(ca, s, i); err != nil {
			return err
		}
		ds[i] = time.Since(t)
	}
	slices.Sort(ds)
	q := func(p float64) time.Duration { return ds[int(p*float64(n-1))].Round(time.Microsecond) }
	fmt.Printf("%-9s leaf certificates n=%d p50=%s p90=%s p99=%s max=%s\n",
		label, n, q(0.5), q(0.9), q(0.99), ds[n-1].Round(time.Microsecond))
	return nil
}

func bench(n int) error {
	var e C.x9err
	k := C.x9_se_create(0, C.CString("x09-bench"), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x9_release(k)
	s, err := newSigner(k)
	if err != nil {
		return err
	}
	if err := timeLeaves("enclave", s, n); err != nil {
		return err
	}
	sw, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	return timeLeaves("software", sw, n)
}
