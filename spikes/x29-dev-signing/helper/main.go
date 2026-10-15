// Command x29 is the throwaway helper for X29-dev-signing: a generic
// password and a wrapped Secure Enclave key in the data protection
// keychain, behind the access group in X29_GROUP.
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
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"
)

// variant is set with -ldflags -X so that each build has its own cdhash.
var variant = "?"

func cerr(e *C.x29err) error {
	return fmt.Errorf("%s (status %d)", C.GoString(&e.msg[0]), int(e.status))
}

func main() {
	group := os.Getenv("X29_GROUP")
	exe, _ := os.Executable()
	fmt.Printf("[x29 %s pid %d exe %s group %q] %s\n", variant, os.Getpid(), exe,
		group, strings.Join(os.Args[1:], " "))
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	start := time.Now()
	err := run(group, os.Args[1], os.Args[2:])
	fmt.Printf("[x29 %s] took %s\n", variant, time.Since(start).Round(time.Microsecond))
	if err != nil {
		fmt.Printf("[x29 %s] FAIL: %v\n", variant, err)
		os.Exit(1)
	}
	fmt.Printf("[x29 %s] OK\n", variant)
}

func run(group, cmd string, args []string) error {
	cg := C.CString(group)
	defer C.free(unsafe.Pointer(cg))
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch cmd {
	case "put":
		return put(cg, arg(0), []byte(arg(1)))
	case "get":
		v, err := get(cg, arg(0))
		if err == nil {
			fmt.Printf("  value %q\n", v)
		}
		return err
	case "del":
		var e C.x29err
		if C.x29_del(cg, cs(arg(0)), &e) != 0 {
			return cerr(&e)
		}
		return nil
	case "list":
		var e C.x29err
		if C.x29_list(&e) != 0 {
			return cerr(&e)
		}
		return nil
	case "se-new":
		return seNew(cg, arg(0))
	case "se-sign":
		return seSign(cg, arg(0))
	case "seperm-new":
		return sePermNew(cg, arg(0))
	case "seperm-del":
		var e C.x29err
		if C.x29_se_del(cg, cs(arg(0)), &e) != 0 {
			return cerr(&e)
		}
		return nil
	case "seperm-sign":
		return sePermSign(cg, arg(0))
	}
	return errors.New("unknown command")
}

// cs leaks; the process is short-lived.
func cs(s string) *C.char { return C.CString(s) }

func put(cg *C.char, account string, v []byte) error {
	var e C.x29err
	if len(v) == 0 {
		return errors.New("empty value")
	}
	if C.x29_put(cg, cs(account), (*C.uchar)(&v[0]), C.int(len(v)), &e) != 0 {
		return cerr(&e)
	}
	return nil
}

func get(cg *C.char, account string) ([]byte, error) {
	var e C.x29err
	buf := make([]byte, 4096)
	n := C.x29_get(cg, cs(account), (*C.uchar)(&buf[0]), C.int(len(buf)), &e)
	if n < 0 {
		return nil, cerr(&e)
	}
	return buf[:n], nil
}

func pub(k C.SecKeyRef) ([]byte, error) {
	var e C.x29err
	buf := make([]byte, 128)
	n := C.x29_se_pub(k, (*C.uchar)(&buf[0]), C.int(len(buf)), &e)
	if n < 0 {
		return nil, cerr(&e)
	}
	return buf[:n], nil
}

// signCheck signs a digest with k and verifies it against k's public key.
func signCheck(k C.SecKeyRef) error {
	p, err := pub(k)
	if err != nil {
		return err
	}
	fmt.Printf("  public key %s…\n", hex.EncodeToString(p[:12]))
	d := sha256.Sum256([]byte("x29 " + variant))
	var e C.x29err
	sig := make([]byte, 128)
	t := time.Now()
	n := C.x29_se_sign(k, (*C.uchar)(&d[0]), 32, (*C.uchar)(&sig[0]), C.int(len(sig)), &e)
	if n < 0 {
		return cerr(&e)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), p) //nolint:staticcheck // throwaway
	ok := ecdsa.VerifyASN1(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, d[:], sig[:n])
	fmt.Printf("  signed in %s, verifies: %v\n", time.Since(t).Round(time.Microsecond), ok)
	if !ok {
		return errors.New("signature does not verify")
	}
	return nil
}

// seNew makes an ephemeral key and keeps its token object ID in the item
// <account>, the B23 decision 2 path.
func seNew(cg *C.char, account string) error {
	var e C.x29err
	k := C.x29_se_new(0, cg, cs(account), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x29_release(k)
	buf := make([]byte, 1024)
	n := C.x29_se_oid(k, (*C.uchar)(&buf[0]), C.int(len(buf)), &e)
	if n < 0 {
		return cerr(&e)
	}
	fmt.Printf("  token object ID %d bytes\n", int(n))
	if err := put(cg, account, buf[:n]); err != nil {
		return err
	}
	return signCheck(k)
}

func seSign(cg *C.char, account string) error {
	oid, err := get(cg, account)
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

func sePermNew(cg *C.char, label string) error {
	var e C.x29err
	k := C.x29_se_new(1, cg, cs(label), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x29_release(k)
	return signCheck(k)
}

func sePermSign(cg *C.char, label string) error {
	var e C.x29err
	k := C.x29_se_find(cg, cs(label), &e)
	if k == 0 {
		return cerr(&e)
	}
	defer C.x29_release(k)
	return signCheck(k)
}
