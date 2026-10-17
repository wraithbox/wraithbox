package diskimage

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// The backing path check before the format reads the attached device's
// backing image from the I/O Registry, in this process. On macOS 27
// diskutil has no command that reports it, and hdiutil is outside
// wb-diskfmt's profile (S04-04-vm-processes, "`wb-diskfmt`"). The calls
// go through the trampolines in iokit_darwin_<arch>.s and the runtime's
// libc call helper, as internal/platform/selfsandbox calls libSystem,
// so the binary builds with CGO_ENABLED=0.

//go:cgo_import_dynamic diskimage_IOBSDNameMatching IOBSDNameMatching "/System/Library/Frameworks/IOKit.framework/Versions/A/IOKit"
//go:cgo_import_dynamic diskimage_IOServiceGetMatchingService IOServiceGetMatchingService "/System/Library/Frameworks/IOKit.framework/Versions/A/IOKit"
//go:cgo_import_dynamic diskimage_IORegistryEntrySearchCFProperty IORegistryEntrySearchCFProperty "/System/Library/Frameworks/IOKit.framework/Versions/A/IOKit"
//go:cgo_import_dynamic diskimage_IOObjectRelease IOObjectRelease "/System/Library/Frameworks/IOKit.framework/Versions/A/IOKit"
//go:cgo_import_dynamic diskimage_CFStringCreateWithCString CFStringCreateWithCString "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFRelease CFRelease "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFGetTypeID CFGetTypeID "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFStringGetTypeID CFStringGetTypeID "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFStringGetCString CFStringGetCString "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFDataGetTypeID CFDataGetTypeID "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFDataGetLength CFDataGetLength "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"
//go:cgo_import_dynamic diskimage_CFDataGetBytes CFDataGetBytes "/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation"

// The addresses of the trampolines, set in the assembly.
var (
	ioBSDNameMatchingAddr               uintptr
	ioServiceGetMatchingServiceAddr     uintptr
	ioRegistryEntrySearchCFPropertyAddr uintptr
	ioObjectReleaseAddr                 uintptr
	cfStringCreateWithCStringAddr       uintptr
	cfReleaseAddr                       uintptr
	cfGetTypeIDAddr                     uintptr
	cfStringGetTypeIDAddr               uintptr
	cfStringGetCStringAddr              uintptr
	cfDataGetTypeIDAddr                 uintptr
	cfDataGetLengthAddr                 uintptr
	cfDataGetBytesAddr                  uintptr
)

// syscall6 is the runtime's helper that calls a C function on the system
// stack, the one golang.org/x/sys/unix uses on darwin.
//
//go:linkname syscall6 syscall.syscall6
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno) //nolint:revive // argument-limit: the signature must be syscall.syscall6's, which go:linkname binds

// The constants of IOKit's and CoreFoundation's headers.
const (
	// kIOMainPortDefault is MACH_PORT_NULL.
	kIOMainPortDefault = 0
	// kCFStringEncodingUTF8 in CFString.h.
	kCFStringEncodingUTF8 = 0x08000100
	// kIORegistryIterateRecursively | kIORegistryIterateParents in
	// IOKitKeys.h: search the device and each of its parents.
	searchParents = 1 | 2
	// maxRegistryValue bounds what is read of a property. A path is at
	// most 1024 bytes, and a URL that encodes every byte three times
	// that.
	maxRegistryValue = 4096
)

// registryKeys are the properties read, in order: the URL of the
// DiskImages2 device, then the path of the older disk image driver.
var registryKeys = []string{"DiskImageURL", "image-path"}

// registryBackingPath returns the backing image path of the whole disk
// device, from the first property of registryKeys that the device or a
// parent of it has.
func registryBackingPath(device string) (string, error) {
	svc, err := matchingService(device)
	if err != nil {
		return "", err
	}
	defer call(ioObjectReleaseAddr, svc)
	for _, key := range registryKeys {
		raw, ok := searchProperty(svc, key)
		if !ok {
			continue
		}
		if key == "image-path" {
			if p := absPath(raw); p != "" {
				return p, nil
			}
			return "", fmt.Errorf("diskimage: %s of %s is %q, not an absolute path", key, device, cut(raw))
		}
		return FileURLPath(raw)
	}
	return "", fmt.Errorf("%w for %s (keys %s)", errNoBacking, device, strings.Join(registryKeys, ", "))
}

// matchingService returns the registry entry of the BSD name device.
// The caller releases it.
func matchingService(device string) (uintptr, error) {
	name := cString(device)
	dict := call(ioBSDNameMatchingAddr, kIOMainPortDefault, 0, uintptr(unsafe.Pointer(name)))
	runtime.KeepAlive(name)
	if dict == 0 {
		return 0, fmt.Errorf("diskimage: IOBSDNameMatching(%s) failed", device)
	}
	// IOServiceGetMatchingService consumes the dictionary's reference.
	svc := call(ioServiceGetMatchingServiceAddr, kIOMainPortDefault, dict) & 0xffff_ffff
	if svc == 0 {
		return 0, fmt.Errorf("diskimage: no I/O Registry entry for %s", device)
	}
	return svc, nil
}

// searchProperty reads key from entry or a parent of it, as a string or
// as data. Any other type, or a value over maxRegistryValue bytes, reads
// as absent.
func searchProperty(entry uintptr, key string) (string, bool) {
	keyName := cString(key)
	cfKey := call(cfStringCreateWithCStringAddr, 0, uintptr(unsafe.Pointer(keyName)), kCFStringEncodingUTF8)
	runtime.KeepAlive(keyName)
	if cfKey == 0 {
		return "", false
	}
	defer call(cfReleaseAddr, cfKey)
	plane := cString("IOService")
	v := call(ioRegistryEntrySearchCFPropertyAddr, entry, uintptr(unsafe.Pointer(plane)), cfKey, 0, searchParents)
	runtime.KeepAlive(plane)
	if v == 0 {
		return "", false
	}
	defer call(cfReleaseAddr, v)
	switch call(cfGetTypeIDAddr, v) {
	case call(cfStringGetTypeIDAddr):
		return cfString(v)
	case call(cfDataGetTypeIDAddr):
		return cfData(v)
	}
	return "", false
}

func cfString(s uintptr) (string, bool) {
	buf := make([]byte, maxRegistryValue)
	ok := call(cfStringGetCStringAddr, s, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), kCFStringEncodingUTF8)
	runtime.KeepAlive(buf)
	// The result is a C Boolean: only the low 8 bits are set.
	if ok&0xff == 0 {
		return "", false
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n]), true
}

func cfData(d uintptr) (string, bool) {
	n := call(cfDataGetLengthAddr, d)
	if n == 0 || n > maxRegistryValue {
		return "", false
	}
	buf := make([]byte, n)
	// CFDataGetBytes(d, CFRange{0, n}, buf): the range is two CFIndex
	// fields, passed in two registers on arm64 and amd64.
	call(cfDataGetBytesAddr, d, 0, n, uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	return strings.TrimRight(string(buf), "\x00"), true
}

// call calls the C function at fn with up to six arguments, and returns
// its first result register.
func call(fn uintptr, args ...uintptr) uintptr {
	var a [6]uintptr
	copy(a[:], args)
	r1, _, _ := syscall6(fn, a[0], a[1], a[2], a[3], a[4], a[5])
	return r1
}

// cString returns s as a NUL-terminated C string in Go memory.
func cString(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}
