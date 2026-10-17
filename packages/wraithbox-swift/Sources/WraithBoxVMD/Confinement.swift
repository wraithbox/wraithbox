// wb-vmd's self-sandbox (S04-03-self-sandboxing, "The `wb-vmd` profile",
// SEC12-least-privilege). The profile is compiled in, and its parameters
// are the bundle's real path and the per-user cache directory, both
// worked out before confinement. It is the X25-vmd-sandbox profile
// `vmd-final-narrow.sb`, measured with the X18-vsock-handoff device set.
import Darwin

/// The profile and how wb-vmd applies it.
public enum Confinement {
    /// The `wb-vmd` profile of S04-03-self-sandboxing. It allows reading
    /// and writing the one bundle, the Virtualization service, the system
    /// reads the framework makes, and the sandbox extensions it hands to
    /// its service process. It denies the rest: the user's other files,
    /// the other VMs' bundles among them, the network, starting programs
    /// and other services.
    public static let profile = """
        (version 1)
        (deny default)

        ; The VM bundle: disks, auxiliary storage, identity, saved state.
        (allow file-read* file-write* (subpath (param "BUNDLE")))

        ; The service process that runs the VM.
        (allow mach-lookup (xpc-service-name "com.apple.Virtualization.VirtualMachine"))

        ; Configuration checks. The CPU name goes into a saved state, so a
        ; state restores only in a process that could read it too
        ; (X25-vmd-sandbox, condition 2).
        (allow sysctl-read (sysctl-name "kern.hv_support" "hw.memsize" "machdep.cpu.brand_string"))
        (allow file-read* (literal "/System/Library/CoreServices/SystemVersion.plist"))

        ; The framework's error texts. Without them a configuration error
        ; ends the process in an uncaught exception (X25-vmd-sandbox,
        ; condition 3).
        (allow file-read* (subpath "/System/Library/Frameworks/Virtualization.framework"))

        ; Finding the cache directory, whose value comes from before
        ; confinement.
        (allow file-read-metadata (literal "/var") (literal "/private/var/folders"))

        ; Sandbox extensions the framework hands to its service process.
        (allow generic-issue-extension
          (extension-class
            "com.apple.virtualization.extension.avp.rtc"
            "com.apple.virtualization.extension.fp"
            "com.apple.virtualization.extension.io-surface"
            "com.apple.virtualization.extension.paravirtualized-graphics"
            "com.apple.virtualization.extension.strong-identity"
            "com.apple.virtualization.extension.usb-hci"
            "com.apple.virtualization.extension.videotoolbox"))
        (allow file-issue-extension
          (require-all (subpath (param "BUNDLE")) (extension-class "com.apple.app-sandbox.read")))
        (allow file-issue-extension
          (require-all
            (extension-class "com.apple.app-sandbox.read-write")
            (prefix
              (string-append (param "CACHE") "/com.apple.metal-")
              (string-append (param "CACHE") "/com.apple.paravirtualizedgraphics-"))))
        """

    /// The file the deny probe opens. The profile denies reading it, and
    /// every user can read it unconfined.
    public static let probePath = "/etc/passwd"

    private typealias SandboxInit =
        @convention(c) (
            UnsafePointer<CChar>, UInt64, UnsafePointer<UnsafePointer<CChar>?>?,
            UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>
        ) -> Int32

    private typealias SandboxFreeError = @convention(c) (UnsafeMutablePointer<CChar>?) -> Void

    /// Applies the profile to this process, with the bundle and cache
    /// directory as its parameters. The function is looked up with dlsym
    /// and called with the C calling convention: it has no public header.
    public static func apply(bundle: String, cache: String) throws(StartupRefusal) {
        let values = [("BUNDLE", bundle), ("CACHE", cache)]
        try checkParameters(values)
        guard
            let initSymbol = dlsym(
                UnsafeMutableRawPointer(bitPattern: -2), "sandbox_init_with_parameters")
        else {
            throw StartupRefusal(.sandboxInit, "sandbox_init_with_parameters not found")
        }
        let sandboxInit = unsafeBitCast(initSymbol, to: SandboxInit.self)
        let pairs: [String] = values.flatMap { [$0.0, $0.1] }
        let strings: [UnsafeMutablePointer<CChar>?] = pairs.map { strdup($0) }
        defer {
            for string in strings {
                free(string)
            }
        }
        var parameters: [UnsafePointer<CChar>?] = strings.map { pointer in
            pointer.map { UnsafePointer<CChar>($0) }
        }
        parameters.append(nil)
        var errorText: UnsafeMutablePointer<CChar>?
        let result = parameters.withUnsafeBufferPointer { list in
            sandboxInit(profile, 0, list.baseAddress, &errorText)
        }
        guard result == 0 else {
            let detail = errorText.map { String(cString: $0) } ?? "error \(result)"
            if let freeSymbol = dlsym(UnsafeMutableRawPointer(bitPattern: -2), "sandbox_free_error")
            {
                unsafeBitCast(freeSymbol, to: SandboxFreeError.self)(errorText)
            }
            throw StartupRefusal(.sandboxInit, detail)
        }
    }

    /// Refuses a parameter value that is empty or holds a NUL byte, while
    /// the process is still unconfined (S04-03-self-sandboxing, "Confined
    /// first thing in `main`"). An empty value names no path, and `strdup`
    /// would cut a value at its first NUL, so the profile would get
    /// another path than the one resolved.
    static func checkParameters(_ values: [(String, String)]) throws(StartupRefusal) {
        for (name, value) in values where value.isEmpty || value.utf8.contains(0) {
            throw StartupRefusal(
                .sandboxParameter, "sandbox parameter \(name) is empty or holds a NUL byte")
        }
    }

    /// Tries one operation the profile denies, and refuses unless the
    /// sandbox denied it (S04-03-self-sandboxing, "Confined first thing in
    /// `main`").
    public static func probe(path: String = probePath) throws(StartupRefusal) {
        let fd = open(path, O_RDONLY | O_CLOEXEC)
        let error = errno
        if fd >= 0 {
            close(fd)
        }
        try checkProbe(path: path, opened: fd >= 0, error: error)
    }

    /// A sandbox denial fails with EPERM. Success, or any other error,
    /// such as a missing file, doesn't show that the profile holds.
    static func checkProbe(path: String, opened: Bool, error: Int32) throws(StartupRefusal) {
        guard !opened else {
            throw StartupRefusal(.sandboxProbe, "opened \(path) after confinement")
        }
        guard error == EPERM else {
            let text = String(cString: strerror(error))
            throw StartupRefusal(.sandboxProbe, "open \(path) failed with \(text), not EPERM")
        }
    }
}
