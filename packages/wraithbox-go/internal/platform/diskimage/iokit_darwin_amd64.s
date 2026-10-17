// Trampolines to the IOKit and CoreFoundation functions of
// iokit_darwin.go, called through syscall6. The same as
// iokit_darwin_arm64.s.

#include "textflag.h"

TEXT diskimage_IOBSDNameMatching_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_IOBSDNameMatching(SB)
GLOBL	·ioBSDNameMatchingAddr(SB), RODATA, $8
DATA	·ioBSDNameMatchingAddr(SB)/8, $diskimage_IOBSDNameMatching_trampoline<>(SB)

TEXT diskimage_IOServiceGetMatchingService_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_IOServiceGetMatchingService(SB)
GLOBL	·ioServiceGetMatchingServiceAddr(SB), RODATA, $8
DATA	·ioServiceGetMatchingServiceAddr(SB)/8, $diskimage_IOServiceGetMatchingService_trampoline<>(SB)

TEXT diskimage_IORegistryEntrySearchCFProperty_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_IORegistryEntrySearchCFProperty(SB)
GLOBL	·ioRegistryEntrySearchCFPropertyAddr(SB), RODATA, $8
DATA	·ioRegistryEntrySearchCFPropertyAddr(SB)/8, $diskimage_IORegistryEntrySearchCFProperty_trampoline<>(SB)

TEXT diskimage_IOObjectRelease_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_IOObjectRelease(SB)
GLOBL	·ioObjectReleaseAddr(SB), RODATA, $8
DATA	·ioObjectReleaseAddr(SB)/8, $diskimage_IOObjectRelease_trampoline<>(SB)

TEXT diskimage_CFStringCreateWithCString_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFStringCreateWithCString(SB)
GLOBL	·cfStringCreateWithCStringAddr(SB), RODATA, $8
DATA	·cfStringCreateWithCStringAddr(SB)/8, $diskimage_CFStringCreateWithCString_trampoline<>(SB)

TEXT diskimage_CFRelease_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFRelease(SB)
GLOBL	·cfReleaseAddr(SB), RODATA, $8
DATA	·cfReleaseAddr(SB)/8, $diskimage_CFRelease_trampoline<>(SB)

TEXT diskimage_CFGetTypeID_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFGetTypeID(SB)
GLOBL	·cfGetTypeIDAddr(SB), RODATA, $8
DATA	·cfGetTypeIDAddr(SB)/8, $diskimage_CFGetTypeID_trampoline<>(SB)

TEXT diskimage_CFStringGetTypeID_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFStringGetTypeID(SB)
GLOBL	·cfStringGetTypeIDAddr(SB), RODATA, $8
DATA	·cfStringGetTypeIDAddr(SB)/8, $diskimage_CFStringGetTypeID_trampoline<>(SB)

TEXT diskimage_CFStringGetCString_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFStringGetCString(SB)
GLOBL	·cfStringGetCStringAddr(SB), RODATA, $8
DATA	·cfStringGetCStringAddr(SB)/8, $diskimage_CFStringGetCString_trampoline<>(SB)

TEXT diskimage_CFDataGetTypeID_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFDataGetTypeID(SB)
GLOBL	·cfDataGetTypeIDAddr(SB), RODATA, $8
DATA	·cfDataGetTypeIDAddr(SB)/8, $diskimage_CFDataGetTypeID_trampoline<>(SB)

TEXT diskimage_CFDataGetLength_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFDataGetLength(SB)
GLOBL	·cfDataGetLengthAddr(SB), RODATA, $8
DATA	·cfDataGetLengthAddr(SB)/8, $diskimage_CFDataGetLength_trampoline<>(SB)

TEXT diskimage_CFDataGetBytes_trampoline<>(SB),NOSPLIT,$0-0
	JMP	diskimage_CFDataGetBytes(SB)
GLOBL	·cfDataGetBytesAddr(SB), RODATA, $8
DATA	·cfDataGetBytesAddr(SB)/8, $diskimage_CFDataGetBytes_trampoline<>(SB)
