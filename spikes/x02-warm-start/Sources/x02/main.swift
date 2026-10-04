// X02-warm-start spike host tool. Throwaway code, not held to the gates.
//
// Bundle layout (a directory):
//   aux.img hw.bin mid.bin sys.img data.img cfg.json [state.vzvmsave]
//
// Commands:
//   fetch-url                          print the latest supported restore image URL
//   install <bundle> <ipsw> <memGiB>   create a bundle and install macOS
//   validate <bundle>                  validateSaveRestoreSupport
//   boot <bundle> <holdSec>            cold boot, ping, hold, halt
//   cold <bundle> <runs>               N cold boots to vsock pong, halt each
//   cycle <bundle> <runs> <settleSec>  cold boot once, then N save+restore cycles;
//                                      leaves state.vzvmsave in the bundle
//   restore <bundle> <holdSec> [opts]  restore once from state.vzvmsave, ping, net,
//                                      hold, then stop (no save). opts:
//                                      --new-mid  use a fresh machine identifier
//                                      --new-mac  use a fresh MAC address
//                                      --save     save state again before stopping
import Darwin
import Foundation
import Virtualization

// MARK: - helpers

func now() -> Double { Double(clock_gettime_nsec_np(CLOCK_UPTIME_RAW)) / 1e9 }

func log(_ s: String) {
    let ts = ISO8601DateFormatter().string(from: Date())
    FileHandle.standardError.write("[\(ts)] \(s)\n".data(using: .utf8)!)
}

/// One machine-readable result line on stdout.
func result(_ kv: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: kv, options: [.sortedKeys])
    print(String(data: data, encoding: .utf8)!)
    fflush(stdout)
}

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

func fileSize(_ u: URL) -> (logical: Int64, allocated: Int64) {
    var st = stat()
    guard stat(u.path, &st) == 0 else { return (-1, -1) }
    return (Int64(st.st_size), Int64(st.st_blocks) * 512)
}

func createSparse(_ u: URL, gib: Int) throws {
    let fd = open(u.path, O_RDWR | O_CREAT | O_EXCL, 0o644)
    guard fd >= 0 else { throw NSError(domain: "x02", code: Int(errno), userInfo: [NSLocalizedDescriptionKey: "create \(u.path)"]) }
    defer { close(fd) }
    guard ftruncate(fd, off_t(gib) << 30) == 0 else { throw NSError(domain: "x02", code: Int(errno)) }
}

// MARK: - network endpoint (host side of the file-handle attachment)

/// Host end of a SOCK_DGRAM socketpair; a reader thread records frames.
final class NetEndpoint {
    let hostFD: Int32
    let guestHandle: FileHandle
    private let lock = NSLock()
    private var frames: [(t: Double, data: Data)] = []
    private var stopped = false

    init() {
        var fds: [Int32] = [0, 0]
        precondition(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0)
        var sz: Int32 = 1 << 20
        for fd in fds {
            setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &sz, socklen_t(MemoryLayout<Int32>.size))
            setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &sz, socklen_t(MemoryLayout<Int32>.size))
        }
        hostFD = fds[0]
        guestHandle = FileHandle(fileDescriptor: fds[1], closeOnDealloc: true)
        let fd = hostFD
        Thread.detachNewThread { [weak self] in
            var buf = [UInt8](repeating: 0, count: 65536)
            while true {
                let n = read(fd, &buf, buf.count)
                if n <= 0 { return }
                guard let self else { return }
                self.lock.lock()
                self.frames.append((now(), Data(buf[0..<n])))
                let s = self.stopped
                self.lock.unlock()
                if s { return }
            }
        }
    }

    func frameCount() -> Int { lock.lock(); defer { lock.unlock() }; return frames.count }

    /// Time of the first frame containing marker, if any.
    func find(_ marker: String) -> Double? {
        let m = marker.data(using: .utf8)!
        lock.lock(); defer { lock.unlock() }
        return frames.first(where: { $0.data.range(of: m) != nil })?.t
    }

    func close() {
        lock.lock(); stopped = true; lock.unlock()
        Darwin.shutdown(hostFD, SHUT_RDWR)
        Darwin.close(hostFD)
    }
}

// MARK: - configuration

func makeConfig(_ b: Bundle, net: NetEndpoint, newMID: Bool = false, newMAC: Bool = false) throws -> VZVirtualMachineConfiguration {
    let cfg = try b.cfg()
    let plat = VZMacPlatformConfiguration()
    guard let hw = VZMacHardwareModel(dataRepresentation: try Data(contentsOf: b.hw)) else { throw NSError(domain: "x02", code: 1) }
    plat.hardwareModel = hw
    if newMID {
        plat.machineIdentifier = VZMacMachineIdentifier()
    } else {
        guard let mid = VZMacMachineIdentifier(dataRepresentation: try Data(contentsOf: b.mid)) else { throw NSError(domain: "x02", code: 2) }
        plat.machineIdentifier = mid
    }
    plat.auxiliaryStorage = VZMacAuxiliaryStorage(url: b.aux)

    let c = VZVirtualMachineConfiguration()
    c.platform = plat
    c.bootLoader = VZMacOSBootLoader()
    c.cpuCount = cfg.cpus
    c.memorySize = UInt64(cfg.memGiB) << 30

    let gfx = VZMacGraphicsDeviceConfiguration()
    gfx.displays = [VZMacGraphicsDisplayConfiguration(widthInPixels: 1280, heightInPixels: 800, pixelsPerInch: 80)]
    c.graphicsDevices = [gfx]

    func disk(_ u: URL) throws -> VZVirtioBlockDeviceConfiguration {
        let a = try VZDiskImageStorageDeviceAttachment(url: u, readOnly: false, cachingMode: .automatic, synchronizationMode: .full)
        return VZVirtioBlockDeviceConfiguration(attachment: a)
    }
    c.storageDevices = [try disk(b.sys), try disk(b.data)]

    let nic = VZVirtioNetworkDeviceConfiguration()
    nic.attachment = VZFileHandleNetworkDeviceAttachment(fileHandle: net.guestHandle)
    nic.macAddress = newMAC ? VZMACAddress.randomLocallyAdministered() : VZMACAddress(string: cfg.mac)!
    c.networkDevices = [nic]

    c.socketDevices = [VZVirtioSocketDeviceConfiguration()]
    c.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    try c.validate()
    return c
}

// MARK: - VM wrapper

final class Delegate: NSObject, VZVirtualMachineDelegate {
    var onStop: ((Error?) -> Void)?
    func guestDidStop(_ vm: VZVirtualMachine) { log("guest did stop"); onStop?(nil) }
    func virtualMachine(_ vm: VZVirtualMachine, didStopWithError error: Error) { log("vm stopped with error: \(error)"); onStop?(error) }
}

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let net: NetEndpoint
    let delegate = Delegate()
    private var stopCont: CheckedContinuation<Void, Never>?
    private(set) var stopped = false

    init(_ b: Bundle, newMID: Bool = false, newMAC: Bool = false) throws {
        net = NetEndpoint()
        vm = VZVirtualMachine(configuration: try makeConfig(b, net: net, newMID: newMID, newMAC: newMAC))
        vm.delegate = delegate
        delegate.onStop = { [weak self] _ in
            MainActor.assumeIsolated {
                self?.stopped = true
                self?.stopCont?.resume()
                self?.stopCont = nil
            }
        }
    }

    var socket: VZVirtioSocketDevice { vm.socketDevices[0] as! VZVirtioSocketDevice }

    /// Connect to the guest probe and send one request line; retries until deadline.
    func request(_ line: String, deadline: Double, retry: Double = 0.02) async throws -> String {
        var lastErr: Error?
        while now() < deadline {
            do {
                let conn = try await socket.connect(toPort: 1024)
                let fd = conn.fileDescriptor
                let reply: String? = await Task.detached { exchange(fd: fd, line: line, timeout: 5) }.value
                conn.close()
                if let reply { return reply }
            } catch {
                lastErr = error
            }
            try await Task.sleep(nanoseconds: UInt64(retry * 1e9))
        }
        throw lastErr ?? NSError(domain: "x02", code: 3, userInfo: [NSLocalizedDescriptionKey: "no reply to \(line)"])
    }

    func waitStopped(timeout: Double) async -> Bool {
        if stopped || vm.state == .stopped { return true }
        let t = Task { @MainActor in
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                if self.stopped { c.resume() } else { self.stopCont = c }
            }
        }
        let deadline = now() + timeout
        while now() < deadline {
            if stopped || vm.state == .stopped { t.cancel(); return true }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        return false
    }

    /// Ask the guest to halt; hard stop if it does not within timeout.
    func halt(timeout: Double = 120) async {
        _ = try? await request("halt", deadline: now() + 5)
        if await waitStopped(timeout: timeout) { return }
        log("guest did not halt in \(timeout)s, hard stop")
        try? await vm.stop()
    }

    func hardStop() async {
        if vm.canStop { try? await vm.stop() }
        net.close()
    }
}

/// Blocking line exchange on a connected vsock fd (called off the main actor).
func exchange(fd: Int32, line: String, timeout: Double) -> String? {
    let out = Array((line + "\n").utf8)
    guard write(fd, out, out.count) == out.count else { return nil }
    var buf = [UInt8](repeating: 0, count: 256)
    var got = [UInt8]()
    let deadline = now() + timeout
    while now() < deadline {
        var p = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        let r = poll(&p, 1, 100)
        if r < 0 { return nil }
        if r == 0 { continue }
        let n = read(fd, &buf, buf.count)
        if n <= 0 { return nil }
        got += buf[0..<n]
        if got.contains(10) { return String(decoding: got, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines) }
    }
    return nil
}

// MARK: - commands

@MainActor
func fetchURL() async throws {
    let img = try await VZMacOSRestoreImage.latestSupported
    result(["url": img.url.absoluteString, "build": img.buildVersion, "version": "\(img.operatingSystemVersion.majorVersion).\(img.operatingSystemVersion.minorVersion).\(img.operatingSystemVersion.patchVersion)"])
}

@MainActor
func install(_ b: Bundle, ipsw: String, memGiB: Int) async throws {
    let img = try await VZMacOSRestoreImage.image(from: URL(fileURLWithPath: ipsw))
    guard let req = img.mostFeaturefulSupportedConfiguration else { throw NSError(domain: "x02", code: 4, userInfo: [NSLocalizedDescriptionKey: "restore image not supported on this host"]) }
    log("image \(img.buildVersion), min cpu \(req.minimumSupportedCPUCount), min mem \(req.minimumSupportedMemorySize >> 20) MiB")
    try FileManager.default.createDirectory(at: b.dir, withIntermediateDirectories: true)
    try req.hardwareModel.dataRepresentation.write(to: b.hw)
    try VZMacMachineIdentifier().dataRepresentation.write(to: b.mid)
    _ = try VZMacAuxiliaryStorage(creatingStorageAt: b.aux, hardwareModel: req.hardwareModel, options: [])
    try createSparse(b.sys, gib: 64)
    try createSparse(b.data, gib: 8)
    let cfg = BundleCfg(cpus: max(4, req.minimumSupportedCPUCount), memGiB: memGiB, mac: VZMACAddress.randomLocallyAdministered().string)
    try JSONEncoder().encode(cfg).write(to: b.cfgURL)
    let m = try Machine(b)
    let inst = VZMacOSInstaller(virtualMachine: m.vm, restoringFromImageAt: URL(fileURLWithPath: ipsw))
    let obs = inst.progress.observe(\.fractionCompleted, options: [.new]) { p, _ in
        log(String(format: "install %.1f%%", p.fractionCompleted * 100))
    }
    let t0 = now()
    try await inst.install()
    obs.invalidate()
    result(["cmd": "install", "seconds": now() - t0])
    await m.hardStop()
}

@MainActor
func validate(_ b: Bundle) throws {
    let c = try makeConfig(b, net: NetEndpoint())
    do {
        try c.validateSaveRestoreSupport()
        result(["cmd": "validate", "saveRestore": true])
    } catch {
        result(["cmd": "validate", "saveRestore": false, "error": "\(error)"])
    }
}

/// Cold boot to first vsock pong. Returns (machine, seconds, reply).
@MainActor
func coldBoot(_ b: Bundle) async throws -> (Machine, Double, String) {
    let t0 = now()
    let m = try Machine(b)
    try await m.vm.start()
    let tStarted = now()
    let reply = try await m.request("ping", deadline: t0 + 600, retry: 0.1)
    let t = now() - t0
    log(String(format: "cold boot: start %.2fs, pong %.2fs: %@", tStarted - t0, t, reply))
    return (m, t, reply)
}

@MainActor
func netCheck(_ m: Machine, t0: Double) async -> [String: Any] {
    let nonce = UUID().uuidString.prefix(8)
    let tReq = now()
    let reply = (try? await m.request("net \(nonce)", deadline: now() + 10)) ?? "noreply"
    var seen: Double?
    let deadline = now() + 5
    while now() < deadline {
        if let t = m.net.find("x02-net-\(nonce)") { seen = t; break }
        try? await Task.sleep(nanoseconds: 2_000_000)
    }
    var r: [String: Any] = ["netReply": reply, "netFrames": m.net.frameCount()]
    if let seen {
        r["netFrameSeconds"] = seen - t0
        r["netRoundTripMs"] = (seen - tReq) * 1000
    }
    return r
}

@MainActor
func boot(_ b: Bundle, hold: Double) async throws {
    let (m, t, reply) = try await coldBoot(b)
    var r: [String: Any] = ["cmd": "boot", "coldSeconds": t, "reply": reply]
    r.merge(await netCheck(m, t0: now()), uniquingKeysWith: { a, _ in a })
    result(r)
    if hold > 0 { try await Task.sleep(nanoseconds: UInt64(hold * 1e9)) }
    await m.halt()
    m.net.close()
}

@MainActor
func cold(_ b: Bundle, runs: Int) async throws {
    for i in 1...runs {
        let (m, t, reply) = try await coldBoot(b)
        var r: [String: Any] = ["cmd": "cold", "run": i, "coldSeconds": t, "reply": reply]
        r.merge(await netCheck(m, t0: now()), uniquingKeysWith: { a, _ in a })
        let th = now()
        await m.halt()
        r["haltSeconds"] = now() - th
        m.net.close()
        result(r)
    }
}

/// Pause and save m to url. Returns timings.
@MainActor
func save(_ m: Machine, to url: URL) async throws -> [String: Any] {
    try? FileManager.default.removeItem(at: url)
    let t0 = now()
    try await m.vm.pause()
    let tPaused = now()
    try await m.vm.saveMachineStateTo(url: url)
    let tSaved = now()
    let sz = fileSize(url)
    return ["pauseMs": (tPaused - t0) * 1000, "saveSeconds": tSaved - tPaused, "saveTotalSeconds": tSaved - t0,
            "stateBytes": sz.logical, "stateAllocBytes": sz.allocated]
}

/// Restore from url into a fresh Machine, resume, and wait for pong.
@MainActor
func restore(_ b: Bundle, from url: URL, newMID: Bool = false, newMAC: Bool = false) async throws -> (Machine, [String: Any]) {
    let t0 = now()
    let m = try Machine(b, newMID: newMID, newMAC: newMAC)
    let tCfg = now()
    try await m.vm.restoreMachineStateFrom(url: url)
    let tRestored = now()
    try await m.vm.resume()
    let tResumed = now()
    let reply = try await m.request("ping", deadline: t0 + 120, retry: 0.005)
    let tPong = now()
    var r: [String: Any] = ["configMs": (tCfg - t0) * 1000, "restoreSeconds": tRestored - tCfg,
                            "resumeMs": (tResumed - tRestored) * 1000, "pongAfterResumeMs": (tPong - tResumed) * 1000,
                            "restoreToPongSeconds": tPong - t0, "reply": reply]
    r.merge(await netCheck(m, t0: t0), uniquingKeysWith: { a, _ in a })
    return (m, r)
}

@MainActor
func cycle(_ b: Bundle, runs: Int, settle: Double) async throws {
    var (m, t, reply) = try await coldBoot(b)
    result(["cmd": "cycle-boot", "coldSeconds": t, "reply": reply])
    log("settling \(settle)s")
    try await Task.sleep(nanoseconds: UInt64(settle * 1e9))
    let tmp = b.dir.appendingPathComponent("cycle.vzvmsave")
    for i in 1...runs {
        var r: [String: Any] = ["cmd": "cycle", "run": i]
        r.merge(try await save(m, to: tmp), uniquingKeysWith: { a, _ in a })
        let ts = now()
        try await m.vm.stop()
        r["stopMs"] = (now() - ts) * 1000
        m.net.close()
        let (m2, rr) = try await restore(b, from: tmp)
        r.merge(rr, uniquingKeysWith: { a, _ in a })
        result(r)
        m = m2
        try await Task.sleep(nanoseconds: 3_000_000_000)
    }
    // Leave a final saved state in the bundle, VM stopped, disks consistent with it.
    let r = try await save(m, to: b.state)
    try await m.vm.stop()
    m.net.close()
    try? FileManager.default.removeItem(at: tmp)
    result(["cmd": "cycle-final-save"].merging(r, uniquingKeysWith: { a, _ in a }))
}

@MainActor
func restoreCmd(_ b: Bundle, hold: Double, opts: Set<String>) async throws {
    let t0 = now()
    do {
        let (m, r) = try await restore(b, from: b.state, newMID: opts.contains("--new-mid"), newMAC: opts.contains("--new-mac"))
        result(["cmd": "restore", "bundle": b.dir.lastPathComponent, "opts": Array(opts).sorted()].merging(r, uniquingKeysWith: { a, _ in a }))
        if hold > 0 {
            try await Task.sleep(nanoseconds: UInt64(hold * 1e9))
            let again = (try? await m.request("ping", deadline: now() + 5)) ?? "noreply"
            result(["cmd": "restore-hold", "bundle": b.dir.lastPathComponent, "afterHold": again])
        }
        if opts.contains("--save") {
            let s = try await save(m, to: b.state)
            result(["cmd": "restore-resave", "bundle": b.dir.lastPathComponent].merging(s, uniquingKeysWith: { a, _ in a }))
        }
        await m.hardStop()
    } catch {
        result(["cmd": "restore", "bundle": b.dir.lastPathComponent, "opts": Array(opts).sorted(), "error": "\(error)",
                "seconds": now() - t0])
    }
}

// MARK: - main

let args = CommandLine.arguments
Task { @MainActor in
    do {
        switch args.count > 1 ? args[1] : "" {
        case "fetch-url": try await fetchURL()
        case "install": try await install(Bundle(args[2]), ipsw: args[3], memGiB: Int(args[4])!)
        case "validate": try validate(Bundle(args[2]))
        case "boot": try await boot(Bundle(args[2]), hold: Double(args[3])!)
        case "cold": try await cold(Bundle(args[2]), runs: Int(args[3])!)
        case "cycle": try await cycle(Bundle(args[2]), runs: Int(args[3])!, settle: Double(args[4])!)
        case "restore": try await restoreCmd(Bundle(args[2]), hold: Double(args[3])!, opts: Set(args.dropFirst(4)))
        default:
            log("usage: see header of main.swift")
            exit(2)
        }
        exit(0)
    } catch {
        log("error: \(error)")
        result(["error": "\(error)"])
        exit(1)
    }
}
RunLoop.main.run()
