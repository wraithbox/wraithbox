// wb-vmd: the macOS VM provider. Runs guest VMs with the Virtualization
// framework on behalf of wb-hostd and hands it the VM's vsock and network
// file descriptors. See docs/spec/S04-architecture.md and docs/spec/S12-platforms.md.
import Foundation
import WraithBoxVM

print(WraithBoxVersion.line(binary: "wb-vmd"))
FileHandle.standardError.write(Data("wb-vmd: not implemented yet\n".utf8))
exit(2)
