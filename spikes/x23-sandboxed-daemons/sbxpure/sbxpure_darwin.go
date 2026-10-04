// Package sbxpure applies a Seatbelt (SBPL) profile without cgo, calling
// sandbox_init_with_parameters through a libSystem trampoline the same way
// golang.org/x/sys/unix calls libc on darwin (cgo_import_dynamic plus
// the runtime's syscall.syscall6 helper).
package sbxpure

import (
	"errors"
	"syscall"
	"unsafe"
)

//go:cgo_import_dynamic libsandbox_sandbox_init_with_parameters sandbox_init_with_parameters "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libsandbox_sandbox_free_error sandbox_free_error "/usr/lib/libSystem.B.dylib"

var (
	libsandbox_sandbox_init_with_parameters_trampoline_addr uintptr
	libsandbox_sandbox_free_error_trampoline_addr           uintptr
)

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

func cstr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// Apply confines the process. params is a flat key, value list.
func Apply(profile string, params []string) error {
	prof := cstr(profile)
	ptrs := make([]*byte, 0, len(params)+1)
	for _, p := range params {
		ptrs = append(ptrs, cstr(p))
	}
	ptrs = append(ptrs, nil)
	var cerr *byte
	r1, _, _ := syscall_syscall6(libsandbox_sandbox_init_with_parameters_trampoline_addr,
		uintptr(unsafe.Pointer(prof)), 0, uintptr(unsafe.Pointer(&ptrs[0])),
		uintptr(unsafe.Pointer(&cerr)), 0, 0)
	if int32(r1) != 0 {
		msg := "sandbox_init_with_parameters failed"
		if cerr != nil {
			msg += ": " + gostring(cerr)
			syscall_syscall6(libsandbox_sandbox_free_error_trampoline_addr, uintptr(unsafe.Pointer(cerr)), 0, 0, 0, 0, 0)
		}
		return errors.New(msg)
	}
	return nil
}

func gostring(p *byte) string {
	var n int
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
