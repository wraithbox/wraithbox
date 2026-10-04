//go:build darwin && cgo

// Package sbxcgo applies a Seatbelt (SBPL) profile to the calling process
// through cgo, using the private sandbox_init_with_parameters entry point
// of libsystem_sandbox. The public header only documents sandbox_init with
// SANDBOX_NAMED; a raw profile string with flags 0 is the undocumented use
// that Chromium and others rely on.
package sbxcgo

/*
#include <stdlib.h>
#include <stdint.h>
int sandbox_init_with_parameters(const char *profile, uint64_t flags, const char *const parameters[], char **errorbuf);
void sandbox_free_error(char *errorbuf);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Apply confines the process. params is a flat key, value list (like
// sandbox-exec -D key=value), readable in the profile with (param "key").
func Apply(profile string, params []string) error {
	cprof := C.CString(profile)
	defer C.free(unsafe.Pointer(cprof))
	cparams := make([]*C.char, 0, len(params)+1)
	for _, p := range params {
		cs := C.CString(p)
		defer C.free(unsafe.Pointer(cs))
		cparams = append(cparams, cs)
	}
	cparams = append(cparams, nil)
	var cerr *C.char
	rc := C.sandbox_init_with_parameters(cprof, 0, (**C.char)(unsafe.Pointer(&cparams[0])), &cerr)
	if rc != 0 {
		msg := "sandbox_init_with_parameters failed"
		if cerr != nil {
			msg += ": " + C.GoString(cerr)
			C.sandbox_free_error(cerr)
		}
		return errors.New(msg)
	}
	return nil
}
