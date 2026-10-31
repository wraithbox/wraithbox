import Foundation

/// The oldest macOS a Wraith Box host runs (NFR04-host-platforms). The
/// package's deployment target is lower, so that its build and tests run
/// on an older Mac. The programs check this at run time instead: `wb-vmd`
/// before it confines itself, and `wb-dev-bundle` before guest
/// provisioning. `wb setup` refuses the same hosts (MinMacOS in the Go
/// module's internal/setup), and the app bundle's deployment target is
/// this version (scripts/app-bundle.sh).
public enum HostRequirement {
    /// The major version. `#available` takes only a literal, so a check
    /// with it names 27 itself.
    public static let minimumMacOS = 27

    /// Whether a host with this macOS version is supported.
    public static func supports(_ version: OperatingSystemVersion) -> Bool {
        version.majorVersion >= minimumMacOS
    }

    /// This host's macOS version, as "27.0.1".
    public static func describe(_ version: OperatingSystemVersion) -> String {
        "\(version.majorVersion).\(version.minorVersion).\(version.patchVersion)"
    }
}
