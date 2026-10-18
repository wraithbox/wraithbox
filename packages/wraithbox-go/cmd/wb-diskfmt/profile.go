package main

import "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"

// profile is wb-diskfmt's compiled-in Seatbelt profile
// (S04-04-vm-processes, "`wb-diskfmt`"; SEC12-least-privilege). Its one
// parameter, BUNDLE, is the real path of the slot's bundle, worked out
// before confinement. diskutil runs as a child and inherits it.
//
// It allows:
//   - reading and writing the data disk's two names in that bundle,
//     data.wbdisk.tmp and data.wbdisk, and reading the bundle
//     directory's metadata. The bundle's other files, the system disk,
//     auxiliary storage and saved state among them, are guest-written,
//     and the shim can't open them (SEC15-no-guest-disk-mount);
//   - the inherited descriptors, which need no rule;
//   - starting /usr/sbin/diskutil, and no other program, with the
//     system's own libraries and settings read-only;
//   - the metadata of the /dev/disk* nodes, not their contents;
//   - the services diskutil reaches, Authorization Services among them
//     (I353). The backing path check reads the I/O Registry, which
//     needs no rule.
//
// It denies the network, every other user file and the user database
// files, so the deny probe's read of /etc/passwd fails
// (internal/platform/selfsandbox).
//
// The service and device rules were measured on macOS 27 with the
// opt-in host test (TestHostFormat), which runs a confined format: the
// format fails without any one of them (I228). A rule beyond this list
// is a change to S04-04-vm-processes first.
var profile = selfsandbox.Profile{
	Name: "wb-diskfmt",
	Text: `(version 1)
(deny default)

; The image under its temporary and final names in the VM's bundle
; (internal/vmbundle), and nothing else in it.
(allow file-read* file-write*
  (literal (string-append (param "BUNDLE") "/data.wbdisk.tmp"))
  (literal (string-append (param "BUNDLE") "/data.wbdisk")))
(allow file-read-metadata (literal (param "BUNDLE")))

; diskutil, at its fixed path, and no other program.
(allow process-fork)
(allow process-exec (literal "/usr/sbin/diskutil"))

; What diskutil reads to start and run: system libraries, frameworks
; and settings, read-only.
(allow file-read*
  (literal "/usr/sbin/diskutil")
  (subpath "/System/Library")
  (subpath "/System/Volumes/Preboot/Cryptexes/OS")
  (subpath "/usr/lib")
  (subpath "/private/var/db/dyld")
  (subpath "/private/var/db/timezone")
  (literal "/private/etc/localtime")
  (subpath "/Library/Preferences/Logging")
  (literal "/dev/null")
  (literal "/dev/urandom"))
(allow file-read-metadata
  (literal "/")
  (literal "/usr")
  (literal "/usr/sbin")
  (literal "/private")
  (literal "/private/var")
  (literal "/private/etc")
  (literal "/System")
  (literal "/Library")
  (literal "/dev"))
; dyld reads the root directory's entries to find the shared cache.
(allow file-read-data (literal "/"))
; diskutil stats a disk's node, and its slices' nodes, before it acts
; on the disk or lists its APFS container. Metadata only: no disk's
; contents.
(allow file-read-metadata (regex #"^/dev/disk[0-9]+(s[0-9]+)?$"))

; The services diskutil reaches, measured on macOS 27 with
; TestHostFormat: without any one of them a format fails. The disk image
; controller and Authorization Services are for the attach, and
; storagekitd.dm for the format.
(allow mach-lookup (xpc-service-name "com.apple.diskimagescontroller"))
(allow mach-lookup
  (global-name
    "com.apple.authd"
    "com.apple.DiskArbitration.diskarbitrationd"
    "com.apple.storagekitd"
    "com.apple.storagekitd.dm"))
`,
}
