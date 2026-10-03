// wb-hostd: the host daemon. Owns VM lifecycle, sessions, the git gateway and
// policy. See docs/spec/004-architecture.md and docs/spec/006-vm-lifecycle.md.
import Foundation
import WraithBoxHost

print(WraithBoxVersion.line(binary: "wb-hostd"))
FileHandle.standardError.write(Data("wb-hostd: not implemented yet\n".utf8))
exit(2)
