// X18-vsock-handoff spike: a wb-vmd stand-in. Throwaway code, not held to the gates.
//
// It owns the VZVirtualMachine and hands descriptors to a Go process
// (hostrecv, the wb-hostd / wb-netd stand-in) over a Unix socket with
// SCM_RIGHTS. The Go process then speaks gRPC over the vsock descriptor and
// ICMPv6 over the network descriptor. This tool never reads or writes guest
// bytes on a handed-off descriptor itself.
//
// Bundle layout (a directory), as in X02-warm-start:
//   aux.img hw.bin mid.bin sys.img data.img cfg.json [state.vzvmsave]
//
// Commands:
//   install <bundle> <ipsw> <memGiB>       create a bundle and install macOS
//   provision <bundle> <user> <pass> <min> first boot with VZMacGuestProvisioningOptions,
//                                          NAT network, Remote Login on; runs until the
//                                          guest shuts down or <min> minutes pass
//   handoff <bundle> <recvSock> <out.jsonl-tag>
//                                          cold boot, run the hand-off experiments,
//                                          then the save/restore experiments
//   prepare <bundle> <recvSock> <settleSec>
//                                          cold boot, wait, save state.vzvmsave, stop
//   restore <bundle> <recvSock> [--keep]   restore from state.vzvmsave, time to the
//                                          guest listener answering gRPC through Go
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

func err(_ s: String) -> NSError { NSError(domain: "x18", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

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

func createSparse(_ u: URL, gib: Int) throws {
    let fd = open(u.path, O_RDWR | O_CREAT | O_EXCL, 0o644)
    guard fd >= 0 else { throw err("create \(u.path): errno \(errno)") }
    defer { close(fd) }
    guard ftruncate(fd, off_t(gib) << 30) == 0 else { throw err("ftruncate errno \(errno)") }
}

/// Describe a descriptor: socket family and type, or file type.
func describeFD(_ fd: Int32) -> [String: Any] {
    var r: [String: Any] = ["fd": fd]
    var st = stat()
    if fstat(fd, &st) != 0 { r["fstatErrno"] = errno; return r }
    let fmt = st.st_mode & S_IFMT
    r["mode"] = fmt == S_IFSOCK ? "socket" : String(format: "0%o", fmt)
    if fmt == S_IFSOCK {
        var ss = sockaddr_storage()
        var len = socklen_t(MemoryLayout<sockaddr_storage>.size)
        let rc = withUnsafeMutablePointer(to: &ss) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &len) } }
        if rc == 0 { r["family"] = Int(ss.ss_family) } else { r["getsocknameErrno"] = errno }
        var ty: Int32 = 0
        var tl = socklen_t(4)
        if getsockopt(fd, SOL_SOCKET, SO_TYPE, &ty, &tl) == 0 { r["sockType"] = Int(ty) }
        var pr = sockaddr_storage()
        var pl = socklen_t(MemoryLayout<sockaddr_storage>.size)
        let prc = withUnsafeMutablePointer(to: &pr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getpeername(fd, $0, &pl) } }
        r["peer"] = prc == 0 ? "family \(pr.ss_family) len \(pl)" : "errno \(errno)"
    }
    return r
}

// MARK: - configuration

enum NIC {
    case fileHandle(FileHandle)
    case nat
}

func makeConfig(_ b: Bundle, nic: NIC) throws -> VZVirtualMachineConfiguration {
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

    func disk(_ u: URL) throws -> VZVirtioBlockDeviceConfiguration {
        let a = try VZDiskImageStorageDeviceAttachment(url: u, readOnly: false, cachingMode: .automatic, synchronizationMode: .full)
        return VZVirtioBlockDeviceConfiguration(attachment: a)
    }
    c.storageDevices = [try disk(b.sys), try disk(b.data)]

    let n = VZVirtioNetworkDeviceConfiguration()
    switch nic {
    case .fileHandle(let h): n.attachment = VZFileHandleNetworkDeviceAttachment(fileHandle: h)
    case .nat: n.attachment = VZNATNetworkDeviceAttachment()
    }
    n.macAddress = VZMACAddress(string: cfg.mac)!
    c.networkDevices = [n]

    c.socketDevices = [VZVirtioSocketDeviceConfiguration()]
    c.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    c.keyboards = [VZUSBKeyboardConfiguration()]
    c.pointingDevices = [VZUSBScreenCoordinatePointingDeviceConfiguration()]
    try c.validate()
    return c
}

/// A SOCK_DGRAM socketpair for the file-handle attachment. The guest end
/// goes into the configuration; the host end is handed to Go.
final class NetPair {
    let hostFD: Int32
    let guestHandle: FileHandle
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
    }
}

// MARK: - VM wrapper

final class Delegate: NSObject, VZVirtualMachineDelegate {
    var stopped = false
    func guestDidStop(_ vm: VZVirtualMachine) { log("guest did stop"); stopped = true }
    func virtualMachine(_ vm: VZVirtualMachine, didStopWithError error: Error) { log("vm stopped with error: \(error)"); stopped = true }
}

/// Accepts guest-initiated vsock connections and keeps them until taken.
final class Listener: NSObject, VZVirtioSocketListenerDelegate {
    var accepted: [VZVirtioSocketConnection] = []
    var acceptedAt: [Double] = []
    func listener(_ listener: VZVirtioSocketListener, shouldAcceptNewConnection c: VZVirtioSocketConnection, from device: VZVirtioSocketDevice) -> Bool {
        log("listener: accepted guest connection src \(c.sourcePort) dst \(c.destinationPort) fd \(c.fileDescriptor)")
        accepted.append(c)
        acceptedAt.append(now())
        return true
    }
}

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let delegate = Delegate()
    let net: NetPair?

    init(_ b: Bundle, nat: Bool = false) throws {
        if nat {
            net = nil
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: .nat))
        } else {
            let p = NetPair()
            net = p
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: .fileHandle(p.guestHandle)))
        }
        vm.delegate = delegate
    }

    var socket: VZVirtioSocketDevice { vm.socketDevices[0] as! VZVirtioSocketDevice }

    func connect(port: UInt32, timeout: Double) async -> Result<VZVirtioSocketConnection, Error> {
        final class Box { var r: Result<VZVirtioSocketConnection, Error>? }
        let box = Box()
        let start = now()
        socket.connect(toPort: port) { box.r = $0 }
        while box.r == nil && now() - start < timeout { try? await Task.sleep(nanoseconds: 500_000) }
        return box.r ?? .failure(err("connect timeout"))
    }

    /// Retry connect until it succeeds. Returns the connection and the time it was made.
    func connectUntil(port: UInt32, deadline: Double) async -> (VZVirtioSocketConnection, Double, Int)? {
        var tries = 0
        while now() < deadline {
            tries += 1
            if case .success(let c) = await connect(port: port, timeout: 1) { return (c, now(), tries) }
            try? await Task.sleep(nanoseconds: 5_000_000)
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

// MARK: - the Go side (hostrecv), over a Unix stream socket

/// Client of hostrecv. One request at a time: a JSON line, with an optional
/// descriptor attached as SCM_RIGHTS, answered by one JSON line.
final class Recv: @unchecked Sendable {
    let fd: Int32
    private var buf: [UInt8] = []

    init(path: String) throws {
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { throw err("path too long") }
        withUnsafeMutableBytes(of: &addr.sun_path) { p in for (i, b) in bytes.enumerated() { p[i] = b } }
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let rc = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
        guard rc == 0 else { throw err("connect \(path): errno \(errno)") }
    }

    /// Send one line, with fd attached when given. CMSG layout on Darwin:
    /// cmsghdr is 12 bytes, data aligned to 4, so CMSG_SPACE(4) = CMSG_LEN(4) = 16.
    func send(_ line: String, fd passFD: Int32?) throws {
        var data = Array((line + "\n").utf8)
        let n: Int = try data.withUnsafeMutableBytes { dp in
            var iov = iovec(iov_base: dp.baseAddress, iov_len: dp.count)
            var msg = msghdr()
            msg.msg_iov = withUnsafeMutablePointer(to: &iov) { $0 }
            msg.msg_iovlen = 1
            if let passFD {
                var control = [UInt8](repeating: 0, count: 16)
                return try control.withUnsafeMutableBytes { cp in
                    cp.storeBytes(of: UInt32(16), toByteOffset: 0, as: UInt32.self)  // cmsg_len
                    cp.storeBytes(of: SOL_SOCKET, toByteOffset: 4, as: Int32.self)  // cmsg_level
                    cp.storeBytes(of: SCM_RIGHTS, toByteOffset: 8, as: Int32.self)  // cmsg_type
                    cp.storeBytes(of: passFD, toByteOffset: 12, as: Int32.self)
                    msg.msg_control = cp.baseAddress
                    msg.msg_controllen = 16
                    return withUnsafeMutablePointer(to: &iov) { ip in
                        msg.msg_iov = ip
                        return sendmsg(fd, &msg, 0)
                    }
                }
            }
            return withUnsafeMutablePointer(to: &iov) { ip in
                msg.msg_iov = ip
                return sendmsg(fd, &msg, 0)
            }
        }
        guard n == data.count else { throw err("sendmsg: \(n) errno \(errno)") }
    }

    func readLine() throws -> [String: Any] {
        while true {
            if let i = buf.firstIndex(of: 0x0a) {
                let line = Array(buf[..<i])
                buf.removeFirst(i + 1)
                return (try JSONSerialization.jsonObject(with: Data(line))) as? [String: Any] ?? [:]
            }
            var tmp = [UInt8](repeating: 0, count: 65536)
            let n = read(fd, &tmp, tmp.count)
            guard n > 0 else { throw err("hostrecv closed: \(n) errno \(errno)") }
            buf += tmp[..<n]
        }
    }

    /// Blocking request on the calling thread.
    func callSync(_ req: [String: Any], fd passFD: Int32? = nil) -> [String: Any] {
        do {
            let d = try JSONSerialization.data(withJSONObject: req, options: [.sortedKeys])
            try send(String(data: d, encoding: .utf8)!, fd: passFD)
            return try readLine()
        } catch {
            return ["error": "\(error)"]
        }
    }

    /// Request on a background thread, so the main thread (and VZ's
    /// callbacks on it) keep running.
    func call(_ req: [String: Any], fd passFD: Int32? = nil) async -> [String: Any] {
        nonisolated(unsafe) let r = req
        let res = await withCheckedContinuation { (k: CheckedContinuation<NSDictionary, Never>) in
            DispatchQueue.global().async { k.resume(returning: self.callSync(r, fd: passFD) as NSDictionary) }
        }
        return res as? [String: Any] ?? [:]
    }
}

// MARK: - commands

@MainActor
func install(_ b: Bundle, ipsw: String, memGiB: Int) async throws {
    let img = try await VZMacOSRestoreImage.image(from: URL(fileURLWithPath: ipsw))
    guard let req = img.mostFeaturefulSupportedConfiguration else { throw err("restore image not supported on this host") }
    log("image \(img.buildVersion)")
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
    var lastPct = -1
    let obs = inst.progress.observe(\.fractionCompleted, options: [.new]) { p, _ in
        let pct = Int(p.fractionCompleted * 100)
        if pct / 10 != lastPct / 10 { log("install \(pct)%"); lastPct = pct }
    }
    let t0 = now()
    try await inst.install()
    obs.invalidate()
    result(["cmd": "install", "seconds": now() - t0, "mac": cfg.mac])
    await m.hardStop()
}

/// First boot with guest provisioning: a user, Remote Login on, NAT network.
/// The spike's setup script then copies the guest daemon in over SSH and
/// shuts the guest down.
@MainActor
func provision(_ b: Bundle, user: String, pass: String, minutes: Double) async throws {
    let m = try Machine(b, nat: true)
    let p = VZMacGuestProvisioningOptions()
    p.fullName = "x18 spike"
    p.username = user
    p.password = pass
    p.logsInAutomatically = false
    p.enablesRemoteLogin = true
    let opts = VZMacOSVirtualMachineStartOptions()
    try opts.setGuestProvisioning(p)
    let t0 = now()
    try await m.vm.start(options: opts)
    result(["cmd": "provision", "event": "started", "seconds": now() - t0, "mac": try b.cfg().mac])
    let stopped = await m.waitStopped(timeout: minutes * 60)
    result(["cmd": "provision", "event": stopped ? "guest stopped" : "timeout", "seconds": now() - t0])
    if !stopped { await m.hardStop() }
}

/// Pause and save m to url.
@MainActor
func save(_ m: Machine, to url: URL) async throws -> [String: Any] {
    try? FileManager.default.removeItem(at: url)
    let t0 = now()
    try await m.vm.pause()
    let tp = now()
    try await m.vm.saveMachineStateTo(url: url)
    return ["pauseMs": (tp - t0) * 1000, "saveSeconds": now() - tp]
}

let guestPort: UInt32 = 1024
let hostPort: UInt32 = 2048

/// Cold boot, then every hand-off experiment, then save and restore with
/// open connections. Each step is one JSON line on stdout.
@MainActor
func handoff(_ b: Bundle, recvPath: String) async throws {
    let rc = try Recv(path: recvPath)
    func step(_ name: String, _ r: [String: Any]) { result(["step": name].merging(r) { a, _ in a }) }

    // --- cold boot
    let t0 = now()
    let m = try Machine(b)
    let lst = Listener()
    let vl = VZVirtioSocketListener()
    vl.delegate = lst
    m.socket.setSocketListener(vl, forPort: hostPort)
    try await m.vm.start()
    step("boot", ["startSeconds": now() - t0])

    // --- part 3: NIC host end to Go; this process closes its copy at once.
    let netFD = m.net!.hostFD
    step("net-describe", describeFD(netFD))
    var r = await rc.call(["op": "adopt-net", "id": "n1", "waitMs": 120_000], fd: netFD)
    close(netFD)
    step("net-adopt", r.merging(["swiftClosedHostEnd": true]) { a, _ in a })

    // --- part 1 + 2: host-initiated connection to the guest listener.
    guard let (c1, tc1, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 300) else {
        step("connect", ["error": "guest listener never answered"]); await m.hardStop(); return
    }
    step("connect", ["seconds": tc1 - t0, "tries": tries, "src": c1.sourcePort, "dst": c1.destinationPort].merging(describeFD(c1.fileDescriptor)) { a, _ in a })

    // 2a: pass, keep the connection object alive and open.
    r = await rc.call(["op": "adopt-vsock", "id": "c1"], fd: c1.fileDescriptor)
    step("c1-adopt-kept", r)
    r = await rc.call(["op": "watch", "id": "c1"])
    step("c1-watch-start", r)

    // 2b: pass, then close() the VZVirtioSocketConnection in this process.
    if case .success(let c2) = await m.connect(port: guestPort, timeout: 5) {
        r = await rc.call(["op": "adopt-vsock", "id": "c2"], fd: c2.fileDescriptor)
        step("c2-adopt", r)
        c2.close()
        step("c2-closed-in-swift", ["fdAfterClose": c2.fileDescriptor])
        try? await Task.sleep(nanoseconds: 500_000_000)
        r = await rc.call(["op": "check", "id": "c2"])
        step("c2-check-after-close", r)
    }

    // 2c: pass, then drop the last reference without close().
    do {
        var c3: VZVirtioSocketConnection? = nil
        if case .success(let c) = await m.connect(port: guestPort, timeout: 5) { c3 = c }
        if let fd3 = c3?.fileDescriptor {
            r = await rc.call(["op": "adopt-vsock", "id": "c3"], fd: fd3)
            step("c3-adopt", r)
            c3 = nil
            step("c3-released-in-swift", ["fdStillOpenInSwift": fcntl(fd3, F_GETFD) != -1])
            try? await Task.sleep(nanoseconds: 500_000_000)
            r = await rc.call(["op": "check", "id": "c3"])
            step("c3-check-after-release", r)
        }
    }

    // 2d: this process's main thread blocked while Go uses c1.
    r = rc.callSync(["op": "check", "id": "c1", "bulkBytes": 8 << 20])
    step("c1-check-main-thread-blocked", r)

    // 2e: guest-initiated connection to the host listener, passed to Go.
    let before = lst.accepted.count
    async let dial = rc.call(["op": "guest-dial", "id": "c1", "port": Int(hostPort)])
    let dl = now() + 10
    while lst.accepted.count == before && now() < dl { try? await Task.sleep(nanoseconds: 1_000_000) }
    if lst.accepted.count > before {
        let g1 = lst.accepted.removeLast()
        r = await rc.call(["op": "adopt-vsock", "id": "g1", "reverse": true], fd: g1.fileDescriptor)
        step("g1-adopt", r.merging(describeFD(g1.fileDescriptor)) { a, _ in a })
        g1.close()
        try? await Task.sleep(nanoseconds: 500_000_000)
        r = await rc.call(["op": "check", "id": "g1"])
        step("g1-check-after-close", r)
    } else {
        step("g1-adopt", ["error": "no guest connection accepted"])
    }
    step("guest-dial", await dial)

    // 2f: this whole process stopped (SIGSTOP, sent by hostrecv, which
    // continues it afterwards) while Go uses c1 and n1: is any of our
    // code in the data path?
    step("c1-n1-check-wbvmd-stopped", await rc.call(["op": "stop-peer-and-check", "id": "c1", "net": "n1", "bulkBytes": 8 << 20]))
    try? await Task.sleep(nanoseconds: 2_000_000_000)
    step("c1-check", await rc.call(["op": "check", "id": "c1"]))
    step("n1-check", await rc.call(["op": "net", "id": "n1"]))

    // --- part 4: save and restore with c1 (and its Watch stream) and n1 open.
    try await m.vm.pause()
    step("paused", [:])
    step("c1-check-while-paused", await rc.call(["op": "check", "id": "c1", "timeoutMs": 2000]))
    step("n1-check-while-paused", await rc.call(["op": "net", "id": "n1", "waitMs": 2000]))
    let tmpState = b.dir.appendingPathComponent("handoff.vzvmsave")
    try? FileManager.default.removeItem(at: tmpState)
    do {
        let ts = now()
        try await m.vm.saveMachineStateTo(url: tmpState)
        step("save-with-open-connections", ["seconds": now() - ts])
    } catch {
        step("save-with-open-connections", ["error": "\(error)"])
    }
    try await m.vm.resume()
    step("resumed-same-vm", [:])
    step("c1-check-after-resume", await rc.call(["op": "check", "id": "c1"]))
    step("n1-check-after-resume", await rc.call(["op": "net", "id": "n1"]))
    step("c1-watch-status", await rc.call(["op": "watch-status", "id": "c1"]))

    // Save again, stop this VM, restore a second VM from a clone.
    try await m.vm.pause()
    try? FileManager.default.removeItem(at: tmpState)
    let ts2 = now()
    try await m.vm.saveMachineStateTo(url: tmpState)
    step("save-2", ["seconds": now() - ts2])
    // Clone disks + state now, before anything else writes them.
    let clone = b.dir.deletingLastPathComponent().appendingPathComponent(b.dir.lastPathComponent + "-handoff-restore")
    try? FileManager.default.removeItem(at: clone)
    let cp = Process()
    cp.executableURL = URL(fileURLWithPath: "/bin/cp")
    cp.arguments = ["-c", "-R", b.dir.path, clone.path]
    try cp.run()
    cp.waitUntilExit()
    let ts3 = now()
    try await m.vm.stop()
    step("stopped-old-vm", ["stopMs": (now() - ts3) * 1000])
    try? await Task.sleep(nanoseconds: 500_000_000)
    step("c1-check-after-old-vm-stopped", await rc.call(["op": "check", "id": "c1", "timeoutMs": 3000]))
    step("c1-watch-status-after-stop", await rc.call(["op": "watch-status", "id": "c1"]))
    step("n1-check-after-old-vm-stopped", await rc.call(["op": "net", "id": "n1", "waitMs": 2000]))

    let cb = Bundle(clone.path)
    try FileManager.default.moveItem(at: cb.dir.appendingPathComponent("handoff.vzvmsave"), to: cb.state)
    let tr0 = now()
    let m2 = try Machine(cb)
    try await m2.vm.restoreMachineStateFrom(url: cb.state)
    let trr = now()
    try await m2.vm.resume()
    let tres = now()
    step("restored", ["restoreSeconds": trr - tr0, "resumedSeconds": tres - tr0])
    step("c1-check-after-restore", await rc.call(["op": "check", "id": "c1", "timeoutMs": 3000]))
    let net2 = m2.net!.hostFD
    r = await rc.call(["op": "adopt-net", "id": "n2", "waitMs": 10_000], fd: net2)
    close(net2)
    step("n2-adopt-after-restore", r.merging(["secondsSinceResume": now() - tres]) { a, _ in a })
    if let (c4, tc4, tries4) = await m2.connectUntil(port: guestPort, deadline: now() + 60) {
        step("connect-after-restore", ["secondsSinceRestoreStart": tc4 - tr0, "tries": tries4])
        r = await rc.call(["op": "adopt-vsock", "id": "c4"], fd: c4.fileDescriptor)
        step("c4-adopt", r.merging(["secondsSinceRestoreStart": now() - tr0]) { a, _ in a })
        // What the guest saw of c1 and the other connections across the restore.
        step("guest-events-after-restore", await rc.call(["op": "events", "id": "c4"]))
        c4.close()
    } else {
        step("connect-after-restore", ["error": "no answer"])
    }
    await m2.hardStop()
    try? FileManager.default.removeItem(at: clone)
    try? FileManager.default.removeItem(at: tmpState)
    _ = c1  // keep c1 alive to here (2a)
}

/// Cold boot, then half-close in both directions on passed raw descriptors.
@MainActor
func halfclose(_ b: Bundle, recvPath: String) async throws {
    let rc = try Recv(path: recvPath)
    let t0 = now()
    let m = try Machine(b)
    try await m.vm.start()
    close(m.net!.hostFD)
    guard let (c, _, _) = await m.connectUntil(port: guestPort, deadline: t0 + 300) else { throw err("guest listener never answered") }
    result(["step": "adopt-c"].merging(await rc.call(["op": "adopt-vsock", "id": "c"], fd: c.fileDescriptor)) { a, _ in a })
    for (port, op) in [(UInt32(1025), "halfclose-fwd"), (UInt32(1026), "halfclose-rev")] {
        if case .success(let h) = await m.connect(port: port, timeout: 5) {
            let r = await rc.call(["op": op, "id": op], fd: h.fileDescriptor)
            h.close()
            result(["step": op].merging(r) { a, _ in a })
        } else {
            result(["step": op, "error": "connect failed"])
        }
    }
    try? await Task.sleep(nanoseconds: 500_000_000)
    let ev = await rc.call(["op": "events", "id": "c"])
    let half = (ev["result"] as? [[String: Any]] ?? []).filter { ($0["kind"] as? String ?? "").hasPrefix("halfclose") }
    result(["step": "guest-events", "events": half])
    await m.hardStop()
}

/// Cold boot, let the guest settle, save state.vzvmsave, stop.
@MainActor
func prepare(_ b: Bundle, recvPath: String, settle: Double) async throws {
    let t0 = now()
    let m = try Machine(b)
    try await m.vm.start()
    close(m.net!.hostFD)
    guard let (c, tc, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 300) else { throw err("guest listener never answered") }
    let rc = try Recv(path: recvPath)
    let r = await rc.call(["op": "adopt-vsock", "id": "prep"], fd: c.fileDescriptor)
    result(["cmd": "prepare", "coldConnectSeconds": tc - t0, "tries": tries, "adopt": r, "coldGrpcSeconds": now() - t0])
    c.close()
    _ = await rc.call(["op": "close", "id": "prep"])
    try await Task.sleep(nanoseconds: UInt64(settle * 1e9))
    let s = try await save(m, to: b.state)
    try await m.vm.stop()
    result(["cmd": "prepare-save"].merging(s) { a, _ in a })
}

/// Restore once from state.vzvmsave: time from before the VZVirtualMachine
/// is created to the guest listener accepting a vsock connection, and to
/// the Go process getting a gRPC answer over the passed descriptor.
@MainActor
func restoreCmd(_ b: Bundle, recvPath: String, keep: Bool) async throws {
    let rc = try Recv(path: recvPath)
    let t0 = now()
    let m = try Machine(b)
    let tCfg = now()
    try await m.vm.restoreMachineStateFrom(url: b.state)
    let tRestored = now()
    try await m.vm.resume()
    let tResumed = now()
    var r: [String: Any] = ["cmd": "restore", "bundle": b.dir.lastPathComponent, "configMs": (tCfg - t0) * 1000,
                            "restoreSeconds": tRestored - tCfg, "resumeMs": (tResumed - tRestored) * 1000,
                            "resumedSeconds": tResumed - t0]
    let netFD = m.net!.hostFD
    async let netR = rc.call(["op": "adopt-net", "id": "rn", "waitMs": 30_000, "once": true], fd: netFD)
    if let (c, tc, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 60) {
        r["connectSeconds"] = tc - t0
        r["connectTries"] = tries
        let rc2 = try Recv(path: recvPath)  // second connection: the net call is still running on the first
        let a = await rc2.call(["op": "adopt-vsock", "id": "r", "once": true], fd: c.fileDescriptor)
        r["grpcSeconds"] = now() - t0
        r["grpc"] = a
        c.close()
    } else {
        r["connectSeconds"] = NSNull()
    }
    let n = await netR
    close(netFD)
    r["net"] = n
    if let up = n["replyAtUptimeRaw"] as? Double { r["netSeconds"] = up - t0 }
    result(r)
    if !keep { await m.hardStop() }
}

// MARK: - main

let args = CommandLine.arguments
Task { @MainActor in
    do {
        switch args.count > 1 ? args[1] : "" {
        case "install": try await install(Bundle(args[2]), ipsw: args[3], memGiB: Int(args[4])!)
        case "provision": try await provision(Bundle(args[2]), user: args[3], pass: args[4], minutes: Double(args[5])!)
        case "handoff": try await handoff(Bundle(args[2]), recvPath: args[3])
        case "halfclose": try await halfclose(Bundle(args[2]), recvPath: args[3])
        case "prepare": try await prepare(Bundle(args[2]), recvPath: args[3], settle: Double(args[4])!)
        case "restore": try await restoreCmd(Bundle(args[2]), recvPath: args[3], keep: args.contains("--keep"))
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
