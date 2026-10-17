// What wb-vmd does before it confines itself (S04-03-self-sandboxing,
// "`wb-vmd` before it confines itself"): it reads its slot from
// WB_VMD_SLOT, looks up the home directory in the user database, and
// resolves its slot's bundle with realpath, to get its profile
// parameter. The slot checks of S04-04-vm-processes ("Slot checks")
// keep it from confining itself to a bundle its slot doesn't own
// (SEC08-proj-isolation). Nothing here reads anything from the guest or
// wb-hostd.
import Darwin
import WraithBoxVM

/// The rule a refusal at start names. wb-vmd logs it and exits.
public enum StartupRule: String, Sendable, Equatable, CaseIterable {
    /// WB_VMD_SLOT isn't set.
    case slotMissing = "vmd-slot-missing"
    /// WB_VMD_SLOT holds a value that isn't a slot.
    case slotUnknown = "vmd-slot-unknown"
    /// The build slot, which only wb-vmd-install serves, with the install
    /// rules and checks that aren't built yet (S04-04-vm-processes,
    /// "`wb-vmd-install`").
    case slotBuildUnsupported = "vmd-slot-build-unsupported"
    /// The slot's bundle path doesn't exist or isn't a directory.
    case bundleMissing = "vmd-bundle-missing"
    /// The bundle's real path isn't the slot's bundle under the real path
    /// of `<data>`.
    case slotBundleMismatch = "vmd-slot-bundle-mismatch"
    /// The user database gave an empty or relative home directory.
    case homeInvalid = "paths-home-invalid"
    /// The per-user cache directory can't be found.
    case cacheDirectory = "vmd-cache-dir"
    /// A sandbox parameter value is empty or holds a NUL byte.
    case sandboxParameter = "vmd-sandbox-param-invalid"
    /// `sandbox_init_with_parameters` failed.
    case sandboxInit = "vmd-sandbox-init"
    /// An operation the profile denies succeeded after confinement.
    case sandboxProbe = "vmd-sandbox-probe"
    /// wb-vmd was started with a command-line argument.
    case arguments = "vmd-arguments"
    /// An inherited descriptor isn't what the start interface says.
    case descriptor = "vmd-descriptor-invalid"
}

/// Why wb-vmd refused to start.
public struct StartupRefusal: Error, Sendable, Equatable, CustomStringConvertible {
    public let rule: StartupRule
    public let detail: String

    public init(_ rule: StartupRule, _ detail: String) {
        self.rule = rule
        self.detail = detail
    }

    public var description: String {
        "\(rule.rawValue): \(detail)"
    }
}

/// The paths of S04-architecture "Host state" that wb-vmd needs, on
/// macOS (S12-01-platform-interfaces, "Paths").
public enum HostPaths {
    /// `<data>` for a home directory.
    public static func data(home: String) -> String {
        home + "/Library/Application Support/WraithBox"
    }

    /// A slot's bundle, relative to `<data>` (S04-04-vm-processes, "One
    /// `wb-vmd` per VM", "The build bundle").
    public static func bundle(of slot: VMSlot) -> String {
        switch slot {
        case .work: "vms/macos-work.noindex"
        case .isolated: "vms/macos-isolated.noindex"
        case .build: "images/macos-build.noindex"
        }
    }
}

/// The slot, and the bundle it confines itself to.
public struct ResolvedSlot: Sendable, Equatable {
    public let slot: VMSlot
    /// The bundle path as `<data>` names it.
    public let bundlePath: String
    /// The bundle's real path: the profile's BUNDLE parameter.
    public let realBundle: String
}

/// The slot checks of S04-04-vm-processes, "Slot checks".
public enum SlotResolution {
    /// The environment variable the launcher entry sets
    /// (S04-04-vm-processes, "The slot").
    public static let variable = "WB_VMD_SLOT"

    /// Resolves the slot and its bundle, or throws the refusal.
    /// `slotValue` is the variable's value, nil when it isn't set.
    public static func resolve(slotValue: String?, home: String) throws(StartupRefusal)
        -> ResolvedSlot
    {
        let slot = try parseSlot(slotValue)
        guard !home.isEmpty, home.hasPrefix("/") else {
            throw StartupRefusal(.homeInvalid, "home directory \"\(home)\" is not absolute")
        }
        let data = HostPaths.data(home: home)
        let relative = HostPaths.bundle(of: slot)
        let bundlePath = data + "/" + relative
        var info = stat()
        guard stat(bundlePath, &info) == 0, info.st_mode & S_IFMT == S_IFDIR else {
            throw StartupRefusal(.bundleMissing, "\(bundlePath) is not a directory")
        }
        // A bundle that resolves anywhere else, such as a symbolic link to
        // the other slot's bundle, is refused (SEC08-proj-isolation).
        guard let realData = realPath(data), let realBundle = realPath(bundlePath),
            realBundle == realData + "/" + relative
        else {
            throw StartupRefusal(
                .slotBundleMismatch,
                "\(bundlePath) does not resolve to the \(slot.rawValue) slot's bundle")
        }
        return ResolvedSlot(slot: slot, bundlePath: bundlePath, realBundle: realBundle)
    }

    static func parseSlot(_ value: String?) throws(StartupRefusal) -> VMSlot {
        guard let value else {
            throw StartupRefusal(.slotMissing, "\(variable) is not set")
        }
        guard let slot = VMSlot(rawValue: value) else {
            throw StartupRefusal(.slotUnknown, "\(variable) is \"\(value)\"")
        }
        guard slot != .build else {
            throw StartupRefusal(
                .slotBuildUnsupported, "the build slot needs wb-vmd-install, which is not built yet"
            )
        }
        return slot
    }

    /// The home directory from the user database, never from HOME
    /// (S04-03-self-sandboxing).
    public static func homeDirectory() -> String {
        guard let entry = getpwuid(getuid()), let directory = entry.pointee.pw_dir else {
            return ""
        }
        return String(cString: directory)
    }

    /// The per-user cache directory, resolved, without a trailing slash:
    /// the profile's CACHE parameter. libSystem keeps the answer, so the
    /// framework's later lookup works under the profile, which denies the
    /// service behind it (X25-vmd-sandbox, condition 1).
    public static func cacheDirectory() throws(StartupRefusal) -> String {
        var buffer = [CChar](repeating: 0, count: Int(PATH_MAX))
        let length = confstr(_CS_DARWIN_USER_CACHE_DIR, &buffer, buffer.count)
        guard length > 0, length <= buffer.count else {
            throw StartupRefusal(.cacheDirectory, "confstr: \(String(cString: strerror(errno)))")
        }
        let raw = String(
            decoding: buffer.prefix(length - 1).map { UInt8(bitPattern: $0) }, as: UTF8.self)
        guard let resolved = realPath(raw) else {
            throw StartupRefusal(
                .cacheDirectory, "realpath \(raw): \(String(cString: strerror(errno)))")
        }
        return resolved
    }
}

/// realpath(3), or nil when it fails.
func realPath(_ path: String) -> String? {
    guard let resolved = realpath(path, nil) else {
        return nil
    }
    defer { free(resolved) }
    return String(cString: resolved)
}
