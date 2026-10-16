// C11-host-daemons, "Device set": a guest-written bundle file has a name
// that Launch Services doesn't open as a disk image
// (SEC15-no-guest-disk-mount, S06-05-disks, "Names on the host"), and a
// spike bundle that kept its own names is refused.
import Foundation
import Testing
import UniformTypeIdentifiers

@testable import WraithBoxVM

/// Whether a double click on a file of this name could open it as a disk
/// image: its extension maps to a type that some app declares, and that
/// type is a disk image. An extension no app declares maps to a dynamic
/// type, which opens nothing.
func opensAsDiskImage(_ name: String) -> Bool {
    let ext = (name as NSString).pathExtension
    guard let type = UTType(filenameExtension: ext) else {
        return true
    }
    return !type.isDynamic || type.conforms(to: .diskImage)
}

@Suite("Bundle files: names on the host")
struct BundleFileNameTests {
    @Test(
        "a guest-written bundle file has no extension that opens it",
        arguments: BundleFile.allCases.filter(\.isGuestWritten))
    func guestWrittenName(file: BundleFile) {
        #expect(!opensAsDiskImage(file.rawValue))
    }

    @Test(
        "the check sees a disk image extension",
        arguments: ["sys.img", "aux.img", "data.dmg", "disk.iso"])
    func diskImageName(name: String) {
        #expect(opensAsDiskImage(name))
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
