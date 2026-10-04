// X02-warm-start spike host tool. Throwaway code, not held to the gates.
//
// The guest runs nothing of ours: it is a fresh macOS install sitting at
// Setup Assistant. Liveness is measured from the guest kernel:
//   vsock  the guest's vsock driver refuses a connect to an unbound port
//          (an answer from the guest kernel, distinct from "no answer")
//   net    the guest's IPv6 stack answers an ICMPv6 echo to ff02::1 sent
//          into the file-handle attachment (after neighbor discovery of
//          fe80::1, which the host answers)
//
// Bundle layout (a directory):
//   aux.img hw.bin mid.bin sys.img data.img cfg.json [state.vzvmsave]
//
// Commands:
//   fetch-url                          print the latest supported restore image URL
//   install <bundle> <ipsw> <memGiB>   create a bundle and install macOS
//   validate <bundle>                  validateSaveRestoreSupport
//   explore <bundle> <seconds>         cold boot and log every probe outcome and frame
//   cold <bundle> <runs>               N cold boots to vsock and net answers
//   cycle <bundle> <runs> <settleSec>  cold boot once, then N save+restore cycles;
//                                      leaves state.vzvmsave in the bundle
//   restore <bundle> <holdSec> [opts]  restore once from state.vzvmsave, probe,
//                                      hold, probe again, then stop. opts:
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

/// Outcome of one vsock connect attempt.
struct VsockAttempt {
    let start: Double
    let end: Double
    let outcome: String  // "connected", "error <domain> <code>: <desc>", or "timeout"
}

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let net: NetEndpoint
    let delegate = Delegate()
    private(set) var stopped = false
    private var seq: UInt16 = 0

    init(_ b: Bundle, newMID: Bool = false, newMAC: Bool = false) throws {
        net = NetEndpoint()
        vm = VZVirtualMachine(configuration: try makeConfig(b, net: net, newMID: newMID, newMAC: newMAC))
        vm.delegate = delegate
        delegate.onStop = { [weak self] _ in MainActor.assumeIsolated { self?.stopped = true } }
    }

    var socket: VZVirtioSocketDevice { vm.socketDevices[0] as! VZVirtioSocketDevice }

    /// One connect attempt to an unbound vsock port, capped at timeout.
    func vsockAttempt(timeout: Double) async -> VsockAttempt {
        let start = now()
        final class Box { var done = false; var outcome = "timeout" }
        let box = Box()
        socket.connect(toPort: 1024) { res in
            switch res {
            case .success(let c):
                box.outcome = "connected"
                c.close()
            case .failure(let e):
                let ne = e as NSError
                let u = (ne.userInfo[NSUnderlyingErrorKey] as? NSError).map { " underlying \($0.domain) \($0.code)" } ?? ""
                box.outcome = "error \(ne.domain) \(ne.code)\(u): \(ne.localizedDescription)"
            }
            box.done = true
        }
        while !box.done && now() - start < timeout {
            try? await Task.sleep(nanoseconds: 1_000_000)
        }
        return VsockAttempt(start: start, end: now(), outcome: box.outcome)
    }

    /// Repeat vsock attempts until the guest kernel answers (any non-timeout outcome).
    /// Returns the time the answer arrived.
    func vsockAnswer(deadline: Double, attemptTimeout: Double = 0.5) async -> (Double, String)? {
        while now() < deadline {
            let a = await vsockAttempt(timeout: attemptTimeout)
            if a.outcome != "timeout" { return (a.end, a.outcome) }
        }
        return nil
    }

    /// Send echo requests every interval until one is answered. Returns reply time.
    func netAnswer(deadline: Double, interval: Double = 0.02) async -> Double? {
        var sent: [UInt16] = []
        while now() < deadline {
            seq &+= 1
            sent.append(seq)
            net.send(echoRequest(seq: seq))
            let until = now() + interval
            while now() < until {
                for s in sent { if let t = net.echoReply(seq: s) { return t } }
                try? await Task.sleep(nanoseconds: 1_000_000)
            }
        }
        return nil
    }

    func waitStopped(timeout: Double) async -> Bool {
        let deadline = now() + timeout
        while now() < deadline {
            if stopped || vm.state == .stopped { return true }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        return false
    }

    /// Graceful shutdown request, hard stop after timeout. Returns seconds and how.
    func shutdown(timeout: Double = 90) async -> (Double, String) {
        let t0 = now()
        do { try vm.requestStop() } catch { log("requestStop: \(error)") }
        if await waitStopped(timeout: timeout) { net.close(); return (now() - t0, "graceful") }
        log("guest did not stop in \(timeout)s, hard stop")
        try? await vm.stop()
        net.close()
        return (now() - t0, "hard")
    }

    func hardStop() async {
        if vm.canStop { try? await vm.stop() }
        net.close()
    }
}

/// Probe both answers, measured from t0. Runs the two probes concurrently.
@MainActor
func probe(_ m: Machine, t0: Double, deadline: Double) async -> [String: Any] {
    async let v = m.vsockAnswer(deadline: deadline)
    async let n = m.netAnswer(deadline: deadline)
    let (vr, nr) = await (v, n)
    var r: [String: Any] = [:]
    if let (t, o) = vr { r["vsockSeconds"] = t - t0; r["vsockOutcome"] = o } else { r["vsockSeconds"] = NSNull() }
    if let t = nr { r["netSeconds"] = t - t0 } else { r["netSeconds"] = NSNull() }
    return r
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

@MainActor
func explore(_ b: Bundle, seconds: Double) async throws {
    let t0 = now()
    let m = try Machine(b)
    try await m.vm.start()
    log(String(format: "started in %.2fs", now() - t0))
    let end = t0 + seconds
    var last = ""
    async let net: Void = {
        var seq: UInt16 = 1000
        while now() < end {
            seq += 1
            m.net.send(echoRequest(seq: seq))
            try? await Task.sleep(nanoseconds: 500_000_000)
            if let t = m.net.echoReply(seq: seq) { log(String(format: "echo %d reply at %.3fs", seq, t - t0)) }
        }
    }()
    while now() < end {
        let a = await m.vsockAttempt(timeout: 2)
        if a.outcome != last {
            log(String(format: "vsock %.3fs..%.3fs: %@", a.start - t0, a.end - t0, a.outcome))
            last = a.outcome
        }
        try? await Task.sleep(nanoseconds: 50_000_000)
    }
    await net
    var lastSummary = ""
    var count = 0
    for f in m.net.snapshot() {
        if f.summary != lastSummary {
            if count > 1 { log("  ... x\(count)") }
            log(String(format: "frame %.3fs %@ len %d", f.t - t0, f.summary, f.bytes.count))
            lastSummary = f.summary
            count = 1
        } else {
            count += 1
        }
    }
    let (s, how) = await m.shutdown()
    log(String(format: "shutdown %.1fs %@", s, how))
}

/// Boot, wait, then try every vsock port for a listener that accepts.
@MainActor
func scan(_ b: Bundle, wait: Double) async throws {
    let m = try Machine(b)
    try await m.vm.start()
    try await Task.sleep(nanoseconds: UInt64(wait * 1e9))
    var outcomes: [String: Int] = [:]
    var open: [UInt32] = []
    var port: UInt32 = 1
    while port < 65536 {
        var pending = 0
        let batchEnd = min(port + 512, 65536)
        for p in port..<batchEnd {
            pending += 1
            m.socket.connect(toPort: p) { res in
                switch res {
                case .success(let c): open.append(p); c.close(); outcomes["connected", default: 0] += 1
                case .failure(let e): outcomes["\((e as NSError).domain) \((e as NSError).code)", default: 0] += 1
                }
                pending -= 1
            }
        }
        let t = now()
        while pending > 0 && now() - t < 5 { try? await Task.sleep(nanoseconds: 2_000_000) }
        if pending > 0 { outcomes["timeout", default: 0] += pending }
        port = batchEnd
    }
    result(["cmd": "scan", "open": open, "outcomes": outcomes])
    await m.hardStop()
}

/// Cold boot to both answers.
@MainActor
func coldBoot(_ b: Bundle) async throws -> (Machine, [String: Any], Double) {
    let t0 = now()
    let m = try Machine(b)
    try await m.vm.start()
    var r: [String: Any] = ["startSeconds": now() - t0]
    r.merge(await probe(m, t0: t0, deadline: t0 + 300)) { a, _ in a }
    return (m, r, t0)
}

@MainActor
func cold(_ b: Bundle, runs: Int) async throws {
    for i in 1...runs {
        let (m, r0, t0) = try await coldBoot(b)
        var r = r0
        r["cmd"] = "cold"
        r["run"] = i
        // First DHCPDISCOVER: configd (guest userland) has brought the NIC up.
        let deadline = now() + 120
        while now() < deadline {
            if let f = m.net.firstFrame(after: 0, where: { $0.udpDstPort == 67 }) { r["dhcpSeconds"] = f.t - t0; break }
            try? await Task.sleep(nanoseconds: 10_000_000)
        }
        // requestStop has no effect at Setup Assistant; stop hard.
        await m.hardStop()
        result(r)
        try await Task.sleep(nanoseconds: 2_000_000_000)
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

/// Restore from url into a fresh Machine, resume, and wait for both answers.
@MainActor
func restore(_ b: Bundle, from url: URL, newMID: Bool = false, newMAC: Bool = false) async throws -> (Machine, [String: Any]) {
    let t0 = now()
    let m = try Machine(b, newMID: newMID, newMAC: newMAC)
    let tCfg = now()
    try await m.vm.restoreMachineStateFrom(url: url)
    let tRestored = now()
    try await m.vm.resume()
    let tResumed = now()
    var r: [String: Any] = ["configMs": (tCfg - t0) * 1000, "restoreSeconds": tRestored - tCfg,
                            "resumeMs": (tResumed - tRestored) * 1000, "resumedSeconds": tResumed - t0]
    r.merge(await probe(m, t0: t0, deadline: t0 + 60)) { a, _ in a }
    return (m, r)
}

@MainActor
func cycle(_ b: Bundle, runs: Int, settle: Double) async throws {
    var (m, r0, _) = try await coldBoot(b)
    result(["cmd": "cycle-boot"].merging(r0) { a, _ in a })
    log("settling \(settle)s")
    try await Task.sleep(nanoseconds: UInt64(settle * 1e9))
    let tmp = b.dir.appendingPathComponent("cycle.vzvmsave")
    for i in 1...runs {
        var r: [String: Any] = ["cmd": "cycle", "run": i]
        r.merge(try await save(m, to: tmp)) { a, _ in a }
        let ts = now()
        try await m.vm.stop()
        r["stopMs"] = (now() - ts) * 1000
        m.net.close()
        let (m2, rr) = try await restore(b, from: tmp)
        r.merge(rr) { a, _ in a }
        result(r)
        m = m2
        try await Task.sleep(nanoseconds: 3_000_000_000)
    }
    // Leave a final saved state in the bundle, VM stopped, disks consistent with it.
    let r = try await save(m, to: b.state)
    try await m.vm.stop()
    m.net.close()
    try? FileManager.default.removeItem(at: tmp)
    result(["cmd": "cycle-final-save"].merging(r) { a, _ in a })
}

@MainActor
func restoreCmd(_ b: Bundle, hold: Double, opts: Set<String>) async throws {
    let t0 = now()
    let name = b.dir.lastPathComponent
    do {
        let (m, r) = try await restore(b, from: b.state, newMID: opts.contains("--new-mid"), newMAC: opts.contains("--new-mac"))
        result(["cmd": "restore", "bundle": name, "opts": Array(opts).sorted()].merging(r) { a, _ in a })
        if hold > 0 {
            try await Task.sleep(nanoseconds: UInt64(hold * 1e9))
            let t1 = now()
            let again = await probe(m, t0: t1, deadline: t1 + 10)
            result(["cmd": "restore-hold", "bundle": name, "state": "\(m.vm.state.rawValue)"].merging(again) { a, _ in a })
        }
        if opts.contains("--save") {
            let s = try await save(m, to: b.state)
            result(["cmd": "restore-resave", "bundle": name].merging(s) { a, _ in a })
        }
        await m.hardStop()
    } catch {
        result(["cmd": "restore", "bundle": name, "opts": Array(opts).sorted(), "error": "\(error)", "seconds": now() - t0])
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
        case "explore": try await explore(Bundle(args[2]), seconds: Double(args[3])!)
        case "newmid":
            // Give a bundle a fresh machine identifier (invalidates its saved state).
            try VZMacMachineIdentifier().dataRepresentation.write(to: Bundle(args[2]).mid)
        case "scan": try await scan(Bundle(args[2]), wait: Double(args[3])!)
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
