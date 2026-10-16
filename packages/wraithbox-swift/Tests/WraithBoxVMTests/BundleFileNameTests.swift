// C11-host-daemons, "Bundle file names": a guest-written bundle file has
// a name that Launch Services doesn't open as a disk image
// (SEC15-no-guest-disk-mount, S06-05-disks, "Names on the host"), and a
// spike bundle that kept its own names is refused.
import Foundation
import Testing
import UniformTypeIdentifiers

@testable import WraithBoxVM

/// Whether the extension of a file of this name maps to a type that some
/// app or the system declares. An extension nobody declares maps to a
/// dynamic type, which Launch Services opens with nothing, so not as a
/// disk image either. This is stricter than "not a disk image".
func hasDeclaredType(_ name: String) -> Bool {
    let ext = (name as NSString).pathExtension
    guard let type = UTType(filenameExtension: ext) else {
        return true
    }
    return !type.isDynamic
}

@Suite("Bundle files: names on the host")
struct BundleFileNameTests {
    @Test(
        "a guest-written bundle file has an extension with no declared type",
        arguments: BundleFile.allCases.filter(\.isGuestWritten))
    func guestWrittenName(file: BundleFile) {
        #expect(!hasDeclaredType(file.rawValue))
    }

    @Test(
        "a disk image or other declared extension has a declared type",
        arguments: ["sys.img", "aux.img", "data.dmg", "disk.iso", "x.txt"])
    func declaredName(name: String) {
        #expect(hasDeclaredType(name))
    }

    @Test("the system disk, auxiliary storage and data disk are guest-written")
    func guestWrittenFiles() {
        #expect(
            BundleFile.allCases.filter(\.isGuestWritten) == [
                .auxiliaryStorage, .systemDisk, .dataDisk,
            ])
    }

    @Test("a spike bundle with its own file names is refused")
    func spikeNames() throws {
        let bundle = try BundleFixture()
        for file in [BundleFile.auxiliaryStorage, .systemDisk, .dataDisk] {
            try bundle.remove(file)
        }
        for name in ["aux.img", "sys.img", "data.img"] {
            try bundle.write(name)
        }
        let log = LogSink()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(log: log)
        }
        #expect(error?.rule == .bundleFileMissing)
        #expect(
            log.lines.first?.hasPrefix(
                "device-set: refused slot=work rule=bundle-file-missing detail=aux.wbstore:")
                == true)
    }
}
