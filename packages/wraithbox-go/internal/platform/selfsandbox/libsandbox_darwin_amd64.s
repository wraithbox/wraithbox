// Trampolines to sandbox_init_with_parameters and sandbox_free_error in
// libSystem, called through syscall6 (libsandbox_darwin.go). The same as
// libsandbox_darwin_arm64.s.

#include "textflag.h"

TEXT libsandbox_sandbox_init_with_parameters_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libsandbox_sandbox_init_with_parameters(SB)
GLOBL	·sandboxInitTrampolineAddr(SB), RODATA, $8
DATA	·sandboxInitTrampolineAddr(SB)/8, $libsandbox_sandbox_init_with_parameters_trampoline<>(SB)

TEXT libsandbox_sandbox_free_error_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libsandbox_sandbox_free_error(SB)
GLOBL	·sandboxFreeErrorTrampolineAddr(SB), RODATA, $8
DATA	·sandboxFreeErrorTrampolineAddr(SB)/8, $libsandbox_sandbox_free_error_trampoline<>(SB)
