// X25-vmd-sandbox spike: a wb-vmd stand-in that confines itself with a
// Seatbelt profile (sandbox_init_with_parameters) before it touches the
// Virtualization framework. Throwaway code, not held to the gates.
//
// Usage:
//   x25 [--sb <profile.sb>] [-D KEY=VALUE]... <command> <args>
//
// --sb applies the profile in process as the first thing main does, with
// the -D pairs as parameters. Then the probes run (what the confined
// process can still do), then the command.
//
// Commands:
//   probe                                   only the probes
//   install <bundle> <ipsw> <memGiB>        create a bundle and install macOS
//   provision <bundle> <user> <pass> <min>  first boot with provisioning options, NAT
//   boot <bundle> [opts]                    cold boot, wait for the guest's vsock
//                                           listener on port 1024, then stop
//   restore <bundle> [opts]                 restore state.vzvmsave, resume, wait for
//                                           vsock, then stop
// boot/restore options:
//   --save            after vsock answers: pause, save state.vzvmsave, stop
//   --hold <sec>      keep the VM running this long after vsock answers
//   --nat <mac>       add a second NIC with a NAT attachment and this MAC
//   --share <dir>     add a virtio-fs device, tag "x25share", read-write
//   --disk <path>     add a read-only virtio block device on this image
//   --serial <path>   add a virtio console serial port that writes to this file
//   --spice           add the SPICE agent console port with clipboard sharing
//   --mic             add a virtio sound device with host audio input
//
// Bundle layout as in X02-warm-start and X18-vsock-handoff:
//   aux.img hw.bin mid.bin sys.img data.img cfg.json [state.vzvmsave]
import Darwin
import Foundation
import Virtualization

// MARK: - confinement, before anything else

@_silgen_name("sandbox_init_with_parameters")
func sandbox_init_with_parameters(
    _ profile: UnsafePointer<CChar>, _ flags: UInt64,
    _ parameters: UnsafePointer<UnsafePointer<CChar>?>?,
    _ errorbuf: UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>
) -> Int32

func now() -> Double { Double(clock_gettime_nsec_np(CLOCK_UPTIME_RAW)) / 1e9 }

/// One machine-readable result line on stdout.
func result(_ kv: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: kv, options: [.sortedKeys])
    print(String(data: data, encoding: .utf8)!)
    fflush(stdout)
}

func log(_ s: String) {
    FileHandle.standardError.write("[\(String(format: "%.3f", now()))] \(s)\n".data(using: .utf8)!)
}

NSSetUncaughtExceptionHandler { e in
    FileHandle.standardError.write("uncaught \(e.name.rawValue): \(e.reason ?? "?")\n\(e.callStackSymbols.joined(separator: "\n"))\n".data(using: .utf8)!)
}

var argv = Array(CommandLine.arguments.dropFirst())
var profilePath: String?
var params: [(String, String)] = []
while let a = argv.first, a.hasPrefix("-") {
    argv.removeFirst()
    switch a {
    case "--sb": profilePath = argv.removeFirst()
    case "-D":
        let kv = argv.removeFirst()
        let i = kv.firstIndex(of: "=")!
        params.append((String(kv[..<i]), String(kv[kv.index(after: i)...])))
    default: log("unknown flag \(a)"); exit(2)
    }
}

/// The per-user cache directory, resolved before confining. libSystem asks
/// the dirhelper service once and keeps the answer, so the framework's
/// later call works without the lookup.
func userCacheDir() -> String {
    var buf = [CChar](repeating: 0, count: Int(PATH_MAX))
    let n = confstr(_CS_DARWIN_USER_CACHE_DIR, &buf, buf.count)
    return n > 0 ? String(cString: buf) : "?(\(errnoName(errno)))"
}

var confined = false
let preCache = ProcessInfo.processInfo.environment["X25_PRECACHE"] != "0"
if preCache {
    result(["step": "pre-confine", "cacheDir": userCacheDir()])
}
if let profilePath {
    // Read the profile before confining: the profile itself need not allow it.
    let text = try! String(contentsOfFile: profilePath, encoding: .utf8)
    var cstrs: [UnsafeMutablePointer<CChar>?] = []
    for (k, v) in params { cstrs.append(strdup(k)); cstrs.append(strdup(v)) }
    cstrs.append(nil)
    var ebuf: UnsafeMutablePointer<CChar>? = nil
    let t0 = now()
    let rc = cstrs.withUnsafeBufferPointer { bp in
        bp.baseAddress!.withMemoryRebound(to: UnsafePointer<CChar>?.self, capacity: bp.count) { p in
            sandbox_init_with_parameters(text, 0, p, &ebuf)
        }
    }
    let ms = (now() - t0) * 1000
    if rc != 0 {
        let e = ebuf.map { String(cString: $0) } ?? "?"
        result(["step": "sandbox_init", "ok": false, "error": e])
        exit(3)  // fail closed
    }
    confined = true
    result(["step": "sandbox_init", "ok": true, "ms": ms, "profile": profilePath,
            "params": params.map { "\($0.0)=\($0.1)" }])
}

// MARK: - probes: what the confined process can still do

func errnoName(_ e: Int32) -> String { e == 0 ? "ok" : String(cString: strerror(e)) }

func probeOpen(_ path: String, _ flags: Int32) -> String {
    let fd = open(path, flags, 0o644)
    if fd < 0 { return errnoName(errno) }
    close(fd)
    return "ok"
}

func probeConnectLoopback() -> String {
    let fd = socket(AF_INET, SOCK_STREAM, 0)
    if fd < 0 { return "socket: " + errnoName(errno) }
    defer { close(fd) }
    var sin = sockaddr_in()
    sin.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
    sin.sin_family = sa_family_t(AF_INET)
    sin.sin_port = in_port_t(1).bigEndian  // nothing listens: refused if allowed
    sin.sin_addr.s_addr = inet_addr("127.0.0.1")
    let rc = withUnsafePointer(to: &sin) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
    return rc == 0 ? "ok" : errnoName(errno)
}

func probeSpawn() -> String {
    var pid: pid_t = 0
    let argv: [UnsafeMutablePointer<CChar>?] = [strdup("/usr/bin/true"), nil]
    let rc = posix_spawn(&pid, "/usr/bin/true", nil, nil, argv, environ)
    if rc != 0 { return errnoName(rc) }
    var st: Int32 = 0
    waitpid(pid, &st, 0)
    return "ok, status \(st)"
}

func runProbes() {
    let home = NSHomeDirectory()
    result(["step": "probes", "confined": confined,
            "read /etc/passwd": probeOpen("/etc/passwd", O_RDONLY),
            "read ~/.ssh/known_hosts": probeOpen(home + "/.ssh/known_hosts", O_RDONLY),
            "write /tmp/x25-probe": probeOpen("/tmp/x25-probe", O_WRONLY | O_CREAT),
            "tcp connect 127.0.0.1:1": probeConnectLoopback(),
            "spawn /usr/bin/true": probeSpawn()])
}

// MARK: - bundle and configuration

func err(_ s: String) -> NSError { NSError(domain: "x25", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

struct BundleCfg: Codable {
    var cpus: Int
    var memGiB: Int
    var mac: String
}

final class Bundle {
    let dir: URL
    init(_ path: String) { dir = URL(fileURLWithPath: path) }
    var aux: URL { dir.appendingPathComponent("aux.img") }
    var hw: URL { dir.appendingPathComponent("hw.bin") }
    var mid: URL { dir.appendingPathComponent("mid.bin") }
    var sys: URL { dir.appendingPathComponent("sys.img") }
    var data: URL { dir.appendingPathComponent("data.img") }
    var cfgURL: URL { dir.appendingPathComponent("cfg.json") }
    var state: URL { dir.appendingPathComponent("state.vzvmsave") }
    func cfg() throws -> BundleCfg { try JSONDecoder().decode(BundleCfg.self, from: Data(contentsOf: cfgURL)) }
}

struct Opts {
    var save = false
    var hold: Double = 0
    var natMAC: String?
    var share: String?
    var disk: String?
    var serial: String?
    var spice = false
    var mic = false
    var provisioningNAT = false

    init() {}
    init(_ a: [String]) {
        var a = a
        while let x = a.first {
            a.removeFirst()
            switch x {
            case "--save": save = true
            case "--hold": hold = Double(a.removeFirst())!
            case "--nat": natMAC = a.removeFirst()
            case "--share": share = a.removeFirst()
            case "--disk": disk = a.removeFirst()
            case "--serial": serial = a.removeFirst()
            case "--spice": spice = true
            case "--mic": mic = true
            default: log("unknown option \(x)"); exit(2)
            }
        }
    }

    var devices: [String] {
        var d: [String] = []
        if let natMAC { d.append("nat \(natMAC)") }
        if let share { d.append("share \(share)") }
        if let disk { d.append("disk \(disk)") }
        if let serial { d.append("serial \(serial)") }
        if spice { d.append("spice") }
        if mic { d.append("mic") }
        return d
    }
}

func createSparse(_ u: URL, gib: Int) throws {
    let fd = open(u.path, O_RDWR | O_CREAT | O_EXCL, 0o644)
    guard fd >= 0 else { throw err("create \(u.path): \(errnoName(errno))") }
    defer { close(fd) }
    guard ftruncate(fd, off_t(gib) << 30) == 0 else { throw err("ftruncate \(errnoName(errno))") }
}

/// The file-handle NIC: a SOCK_DGRAM socketpair. Nothing reads the host
/// end here; wb-netd would.
final class NetPair {
    let hostFD: Int32
    let guestHandle: FileHandle
    init() {
        var fds: [Int32] = [0, 0]
        precondition(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0)
        hostFD = fds[0]
        guestHandle = FileHandle(fileDescriptor: fds[1], closeOnDealloc: true)
    }
}

func makeConfig(_ b: Bundle, net: NetPair?, o: Opts) throws -> VZVirtualMachineConfiguration {
    let cfg = try b.cfg()
    let plat = VZMacPlatformConfiguration()
    guard let hw = VZMacHardwareModel(dataRepresentation: try Data(contentsOf: b.hw)) else { throw err("hw model") }
    plat.hardwareModel = hw
    guard let mid = VZMacMachineIdentifier(dataRepresentation: try Data(contentsOf: b.mid)) else { throw err("machine id") }
    plat.machineIdentifier = mid
    plat.auxiliaryStorage = VZMacAuxiliaryStorage(url: b.aux)

    let c = VZVirtualMachineConfiguration()
    c.platform = plat
    c.bootLoader = VZMacOSBootLoader()
    c.cpuCount = cfg.cpus
    c.memorySize = UInt64(cfg.memGiB) << 30

    let gfx = VZMacGraphicsDeviceConfiguration()
    gfx.displays = [VZMacGraphicsDisplayConfiguration(widthInPixels: 1280, heightInPixels: 800, pixelsPerInch: 80)]
    c.graphicsDevices = [gfx]

    func disk(_ u: URL, ro: Bool) throws -> VZVirtioBlockDeviceConfiguration {
        let a = try VZDiskImageStorageDeviceAttachment(url: u, readOnly: ro, cachingMode: .automatic, synchronizationMode: .full)
        return VZVirtioBlockDeviceConfiguration(attachment: a)
    }
    c.storageDevices = [try disk(b.sys, ro: false), try disk(b.data, ro: false)]
    if let p = o.disk { c.storageDevices.append(try disk(URL(fileURLWithPath: p), ro: true)) }

    var nics: [VZVirtioNetworkDeviceConfiguration] = []
    let n = VZVirtioNetworkDeviceConfiguration()
    n.macAddress = VZMACAddress(string: cfg.mac)!
    if o.provisioningNAT {
        n.attachment = VZNATNetworkDeviceAttachment()
    } else {
        n.attachment = VZFileHandleNetworkDeviceAttachment(fileHandle: net!.guestHandle)
    }
    nics.append(n)
    if let m = o.natMAC {
        let n2 = VZVirtioNetworkDeviceConfiguration()
        n2.macAddress = VZMACAddress(string: m)!
        n2.attachment = VZNATNetworkDeviceAttachment()
        nics.append(n2)
    }
    c.networkDevices = nics

    if let s = o.share {
        let fs = VZVirtioFileSystemDeviceConfiguration(tag: "x25share")
        fs.share = VZSingleDirectoryShare(directory: VZSharedDirectory(url: URL(fileURLWithPath: s), readOnly: false))
        c.directorySharingDevices = [fs]
    }
    if let p = o.serial {
        let sp = VZVirtioConsoleDeviceSerialPortConfiguration()
        sp.attachment = try VZFileSerialPortAttachment(url: URL(fileURLWithPath: p), append: true)
        c.serialPorts = [sp]
    }
    if o.spice {
        let con = VZVirtioConsoleDeviceConfiguration()
        let port = VZVirtioConsolePortConfiguration()
        port.name = VZSpiceAgentPortAttachment.spiceAgentPortName
        let att = VZSpiceAgentPortAttachment()
        att.sharesClipboard = true
        port.attachment = att
        con.ports[0] = port
        c.consoleDevices = [con]
    }
    if o.mic {
        let snd = VZVirtioSoundDeviceConfiguration()
        let inp = VZVirtioSoundDeviceInputStreamConfiguration()
        inp.source = VZHostAudioInputStreamSource()
        snd.streams = [inp]
        c.audioDevices = [snd]
    }

    c.socketDevices = [VZVirtioSocketDeviceConfiguration()]
    c.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    c.keyboards = [VZUSBKeyboardConfiguration()]
    c.pointingDevices = [VZUSBScreenCoordinatePointingDeviceConfiguration()]
    try c.validate()
    if !o.provisioningNAT { _ = try? c.validateSaveRestoreSupport() }
    return c
}

// MARK: - VM

final class Delegate: NSObject, VZVirtualMachineDelegate {
    var stopped = false
    var stopError: String?
    func guestDidStop(_ vm: VZVirtualMachine) { log("guest did stop"); stopped = true }
    func virtualMachine(_ vm: VZVirtualMachine, didStopWithError error: Error) {
        log("vm stopped with error: \(error)"); stopError = "\(error)"; stopped = true
    }
    func virtualMachine(_ vm: VZVirtualMachine, networkDevice: VZNetworkDevice, attachmentWasDisconnectedWithError error: Error) {
        log("network attachment disconnected: \(error)")
    }
}

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let delegate = Delegate()
    let net: NetPair?

    init(_ b: Bundle, o: Opts) throws {
        net = o.provisioningNAT ? nil : NetPair()
        vm = VZVirtualMachine(configuration: try makeConfig(b, net: net, o: o))
        vm.delegate = delegate
    }

    var socket: VZVirtioSocketDevice { vm.socketDevices[0] as! VZVirtioSocketDevice }

    func connect(port: UInt32, timeout: Double) async -> Result<VZVirtioSocketConnection, Error> {
        final class Box { var r: Result<VZVirtioSocketConnection, Error>? }
        let box = Box()
        let start = now()
        socket.connect(toPort: port) { box.r = $0 }
        while box.r == nil && now() - start < timeout { try? await Task.sleep(nanoseconds: 1_000_000) }
        return box.r ?? .failure(err("connect timeout"))
    }

    func connectUntil(port: UInt32, deadline: Double) async -> (VZVirtioSocketConnection, Double, Int)? {
        var tries = 0
        while now() < deadline && !delegate.stopped {
            tries += 1
            if case .success(let c) = await connect(port: port, timeout: 1) { return (c, now(), tries) }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        return nil
    }

    func waitStopped(timeout: Double) async -> Bool {
        let deadline = now() + timeout
        while now() < deadline {
            if delegate.stopped || vm.state == .stopped { return true }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        return false
    }

    func hardStop() async {
        if vm.canStop { try? await vm.stop() }
    }
}

let guestPort: UInt32 = 1024

func install(_ b: Bundle, ipsw: String, memGiB: Int) async throws {
    let t0 = now()
    let img = try await VZMacOSRestoreImage.image(from: URL(fileURLWithPath: ipsw))
    guard let req = img.mostFeaturefulSupportedConfiguration else { throw err("restore image not supported on this host") }
    result(["step": "restore-image-loaded", "build": img.buildVersion, "seconds": now() - t0])
    try FileManager.default.createDirectory(at: b.dir, withIntermediateDirectories: true)
    try req.hardwareModel.dataRepresentation.write(to: b.hw)
    try VZMacMachineIdentifier().dataRepresentation.write(to: b.mid)
    _ = try VZMacAuxiliaryStorage(creatingStorageAt: b.aux, hardwareModel: req.hardwareModel, options: [])
    try createSparse(b.sys, gib: 64)
    try createSparse(b.data, gib: 8)
    let cfg = BundleCfg(cpus: max(4, req.minimumSupportedCPUCount), memGiB: memGiB, mac: VZMACAddress.randomLocallyAdministered().string)
    try JSONEncoder().encode(cfg).write(to: b.cfgURL)
    result(["step": "bundle-created", "seconds": now() - t0])
    let m = try await MainActor.run { try Machine(b, o: Opts()) }
    let inst = await MainActor.run { VZMacOSInstaller(virtualMachine: m.vm, restoringFromImageAt: URL(fileURLWithPath: ipsw)) }
    var lastPct = -1
    let obs = inst.progress.observe(\.fractionCompleted, options: [.new]) { p, _ in
        let pct = Int(p.fractionCompleted * 100)
        if pct / 10 != lastPct / 10 { log("install \(pct)%"); lastPct = pct }
    }
    try await inst.install()
    obs.invalidate()
    result(["step": "installed", "seconds": now() - t0, "mac": cfg.mac])
    await m.hardStop()
}

@MainActor
func provision(_ b: Bundle, user: String, pass: String, minutes: Double) async throws {
    var o = Opts()
    o.provisioningNAT = true
    let m = try Machine(b, o: o)
    let p = VZMacGuestProvisioningOptions()
    p.fullName = "x25 spike"
    p.username = user
    p.password = pass
    p.logsInAutomatically = false
    p.enablesRemoteLogin = true
    let so = VZMacOSVirtualMachineStartOptions()
    try so.setGuestProvisioning(p)
    let t0 = now()
    try await m.vm.start(options: so)
    result(["step": "provision-started", "seconds": now() - t0])
    let stopped = await m.waitStopped(timeout: minutes * 60)
    result(["step": stopped ? "provision-guest-stopped" : "provision-timeout", "seconds": now() - t0])
    if !stopped { await m.hardStop() }
}

/// After the guest's vsock listener answers: hold, save, stop.
@MainActor
func afterUp(_ m: Machine, _ b: Bundle, _ o: Opts, _ r: inout [String: Any]) async {
    if o.hold > 0 {
        result(["step": "holding", "seconds": o.hold])
        let deadline = now() + o.hold
        while now() < deadline && !m.delegate.stopped { try? await Task.sleep(nanoseconds: 200_000_000) }
    }
    if o.save {
        do {
            try? FileManager.default.removeItem(at: b.state)
            let t0 = now()
            try await m.vm.pause()
            let tp = now()
            try await m.vm.saveMachineStateTo(url: b.state)
            r["pauseMs"] = (tp - t0) * 1000
            r["saveSeconds"] = now() - tp
        } catch {
            r["saveError"] = "\(error)"
        }
    }
    let ts = now()
    await m.hardStop()
    r["stopMs"] = (now() - ts) * 1000
}

@MainActor
func boot(_ b: Bundle, _ o: Opts) async throws {
    var r: [String: Any] = ["step": "boot", "bundle": b.dir.lastPathComponent, "devices": o.devices]
    let t0 = now()
    let m: Machine
    do { m = try Machine(b, o: o) } catch { r["configError"] = "\(error)"; result(r); throw error }
    r["configMs"] = (now() - t0) * 1000
    do { try await m.vm.start() } catch { r["startError"] = "\(error)"; result(r); throw error }
    r["startSeconds"] = now() - t0
    if let (c, tc, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 300) {
        r["vsockSeconds"] = tc - t0
        r["vsockTries"] = tries
        c.close()
    } else {
        r["vsockSeconds"] = NSNull()
        r["stopError"] = m.delegate.stopError ?? NSNull()
    }
    await afterUp(m, b, o, &r)
    result(r)
}

@MainActor
func restore(_ b: Bundle, _ o: Opts) async throws {
    var r: [String: Any] = ["step": "restore", "bundle": b.dir.lastPathComponent, "devices": o.devices]
    let t0 = now()
    let m = try Machine(b, o: o)
    r["configMs"] = (now() - t0) * 1000
    do { try await m.vm.restoreMachineStateFrom(url: b.state) } catch { r["restoreError"] = "\(error)"; result(r); throw error }
    r["restoreSeconds"] = now() - t0
    try await m.vm.resume()
    r["resumedSeconds"] = now() - t0
    if let (c, tc, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 120) {
        r["vsockSeconds"] = tc - t0
        r["vsockTries"] = tries
        c.close()
    } else {
        r["vsockSeconds"] = NSNull()
    }
    await afterUp(m, b, o, &r)
    result(r)
}

// MARK: - main

runProbes()
let cmd = argv.first ?? ""
let rest = Array(argv.dropFirst())
Task { @MainActor in
    do {
        switch cmd {
        case "probe": break
        case "install": try await install(Bundle(rest[0]), ipsw: rest[1], memGiB: Int(rest[2])!)
        case "provision": try await provision(Bundle(rest[0]), user: rest[1], pass: rest[2], minutes: Double(rest[3])!)
        case "boot": try await boot(Bundle(rest[0]), Opts(Array(rest.dropFirst())))
        case "restore": try await restore(Bundle(rest[0]), Opts(Array(rest.dropFirst())))
        default: log("usage: see the header of main.swift"); exit(2)
        }
        exit(0)
    } catch {
        log("error: \(error)")
        result(["step": "error", "error": "\(error)"])
        exit(1)
    }
}
RunLoop.main.run()
