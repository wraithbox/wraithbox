package selfsandbox

import (
	"maps"
	"runtime"
	"slices"
	"syscall"
	"unsafe"
)

// sandbox_init_with_parameters and sandbox_free_error are in libSystem.
// The SDK header doesn't declare the variant with parameters, and marks
// sandbox_init deprecated; both work on macOS 27 (X23-sandboxed-daemons).
// The calls go through the trampolines in libsandbox_darwin_<arch>.s and
// the runtime's libc call helper, as golang.org/x/sys/unix calls libc, so
// the binary builds with CGO_ENABLED=0 (X23-sandboxed-daemons, condition 1).

//go:cgo_import_dynamic libsandbox_sandbox_init_with_parameters sandbox_init_with_parameters "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libsandbox_sandbox_free_error sandbox_free_error "/usr/lib/libSystem.B.dylib"

// The addresses of the trampolines, set in the assembly.
var (
	sandboxInitTrampolineAddr      uintptr
	sandboxFreeErrorTrampolineAddr uintptr
)

// syscall6 is the runtime's helper that calls a C function on the system
// stack, the one golang.org/x/sys/unix uses on darwin.
//
//go:linkname syscall6 syscall.syscall6
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno) //nolint:revive // argument-limit: the signature must be syscall.syscall6's, which go:linkname binds

// maxErrorLen bounds how much of the library's error message is read.
const maxErrorLen = 4096

// sandboxInit calls
//
//	int sandbox_init_with_parameters(const char *profile, uint64_t flags,
//	    const char *const parameters[], char **errorbuf);
//
// with the parameters as a NULL-terminated list of name and value
// strings, in name order. It returns the library's error message and
// false when the call fails. The text, names and values hold no NUL
// byte: checkProfile refused those.
func sandboxInit(text string, params map[string]string) (string, bool) {
	profile := cString(text)
	list := make([]*byte, 0, 2*len(params)+1)
	for _, name := range slices.Sorted(maps.Keys(params)) {
		list = append(list, cString(name), cString(params[name]))
	}
	list = append(list, nil)
	var errbuf *byte

	r1, _, _ := syscall6(sandboxInitTrampolineAddr, uintptr(unsafe.Pointer(profile)), 0, uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&errbuf)), 0, 0)
	// The C strings must stay alive until the call returns. Nothing after
	// it reads them, so without KeepAlive the garbage collector could free
	// them during it.
	runtime.KeepAlive(profile)
	runtime.KeepAlive(list)
	// The result is a C int: only the low 32 bits of the register are set.
	if r1&0xffff_ffff == 0 {
		return "", true
	}
	if errbuf == nil {
		return "no error message", false
	}
	msg := goString(errbuf)
	// sandbox_free_error returns nothing.
	_, _, _ = syscall6(sandboxFreeErrorTrampolineAddr, uintptr(unsafe.Pointer(errbuf)), 0, 0, 0, 0, 0)
	return msg, false
}

// cString returns s as a NUL-terminated C string in Go memory.
func cString(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// goString copies a NUL-terminated C string of at most maxErrorLen bytes.
func goString(p *byte) string {
	n := 0
	for n < maxErrorLen && *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
