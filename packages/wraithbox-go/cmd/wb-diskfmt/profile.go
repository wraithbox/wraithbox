package main

import "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"

// profile is wb-diskfmt's compiled-in Seatbelt profile
// (S04-04-vm-processes, "`wb-diskfmt`"; SEC12-least-privilege). Its one
// parameter, BUNDLE, is the real path of the slot's bundle, worked out
// before confinement. diskutil runs as a child and inherits it.
//
// It allows:
//   - reading and writing that one bundle;
//   - the inherited descriptors, which need no rule;
//   - starting /usr/sbin/diskutil, and no other program, with the
//     system's own libraries and settings read-only;
//   - the DiskImages, Disk Arbitration and disk management services
//     diskutil uses, and reading device properties in the I/O Registry,
//     where the backing path check reads the device's backing image.
//
// It denies the network, every other user file and the user database
// files, so the deny probe's read of /etc/passwd fails
// (internal/platform/selfsandbox).
//
// The service names are those diskutil is known to use. They aren't
// measured under this profile yet: the opt-in host test
// (TestHostFormat) runs a confined format, and I228 records the rules it
// needs. A rule beyond this list is a change to S04-04-vm-processes
// first.
var profile = selfsandbox.Profile{
	Name: "wb-diskfmt",
	Text: `(version 1)
(deny default)

; The VM's bundle: the image under its temporary and final names.
(allow file-read* file-write* (subpath (param "BUNDLE")))

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
(allow sysctl-read)

; The disk services: Disk Arbitration, the disk image service, and disk
; management, which runs the format.
(allow mach-lookup
  (global-name
    "com.apple.DiskArbitration.diskarbitrationd"
    "com.apple.diskimagesiod"
    "com.apple.diskmanagementd"
    "com.apple.storagekitd"))
(allow mach-lookup (xpc-service-name-prefix "com.apple.DiskImages"))
(allow iokit-open
  (iokit-user-client-class "AppleDiskImagesControllerUserClient" "IOHDIXControllerUserClient"))
(allow iokit-get-properties)

; Settings and logging, which every system program reaches.
(allow mach-lookup
  (global-name
    "com.apple.cfprefsd.daemon"
    "com.apple.cfprefsd.agent"
    "com.apple.logd"
    "com.apple.system.logger"
    "com.apple.system.notification_center"))
(allow ipc-posix-shm-read* (ipc-posix-name "apple.shm.notification_center"))
`,
}
