#include "textflag.h"

TEXT libsandbox_sandbox_init_with_parameters_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libsandbox_sandbox_init_with_parameters(SB)
GLOBL	·libsandbox_sandbox_init_with_parameters_trampoline_addr(SB), RODATA, $8
DATA	·libsandbox_sandbox_init_with_parameters_trampoline_addr(SB)/8, $libsandbox_sandbox_init_with_parameters_trampoline<>(SB)

TEXT libsandbox_sandbox_free_error_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libsandbox_sandbox_free_error(SB)
GLOBL	·libsandbox_sandbox_free_error_trampoline_addr(SB), RODATA, $8
DATA	·libsandbox_sandbox_free_error_trampoline_addr(SB)/8, $libsandbox_sandbox_free_error_trampoline<>(SB)
