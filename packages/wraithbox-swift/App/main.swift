// wb-notify: the notification helper, the main executable of
// `Wraith Box.app`. macOS shows a notification under the bundle of the
// process that posts it, so the helper is the app itself
// (docs/spec/S17-install-and-upgrade/S17-01-bundle.md, "The bundle").
//
// A stub until the notification helper contract is written: it prints its
// version line and exits 2, like the other programs that aren't built yet.
// WraithBoxVM/Version.swift is compiled into this target, and the bundle
// build stamps it the way `mise run swift:build` stamps the package's
// programs (S10-02-packaging-and-signing, "One build version").
import Foundation

print(WraithBoxVersion.line(binary: "wb-notify"))
FileHandle.standardError.write(Data("wb-notify: not implemented yet\n".utf8))
exit(2)
