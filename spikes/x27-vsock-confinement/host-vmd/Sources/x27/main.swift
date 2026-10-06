// X27-vsock-confinement spike: a wb-vmd / wb-hostd stand-in. Throwaway code,
// not held to the gates. Based on the X18-vsock-handoff harness.
//
// Boots a macOS guest bundle (as in X02-warm-start / X18-vsock-handoff) with
// a NAT network, so the spike can reach the guest over SSH, and a vsock
// device. Optionally provisions the first boot (VZMacGuestProvisioningOptions:
// a user with Remote Login on).
//
// It registers one host vsock listener on port 2048 (the product registers
// none; this one is here to see whether a non-root guest process can dial
// the host at all). Port 2049 has no listener.
//
// A control socket (Unix stream, one JSON reply line per request line):
//   connect <port>                    connect to the guest port, read one line
//   poll <port> <intervalMs> <durMs>  connect every interval for dur, one reply
//                                     line per attempt, then {"done":true}
//   stop                              stop the VM and exit
//
// Commands:
//   run <bundle> <ctlSock> <minutes> [<user> <pass>]
import Darwin
import Foundation
import Virtualization

func now() -> Double { Double(clock_gettime_nsec_np(CLOCK_UPTIME_RAW)) / 1e9 }
func wall() -> Double { Date().timeIntervalSince1970 }

func log(_ s: String) {
    let ts = ISO8601DateFormatter().string(from: Date())
    FileHandle.standardError.write("[\(ts)] \(s)\n".data(using: .utf8)!)
}

func json(_ kv: [String: Any]) -> String {
    let data = try! JSONSerialization.data(withJSONObject: kv, options: [.sortedKeys])
    return String(data: data, encoding: .utf8)!
}

func result(_ kv: [String: Any]) {
    print(json(kv))
    fflush(stdout)
}

func err(_ s: String) -> NSError { NSError(domain: "x27", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

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
    func cfg() throws -> BundleCfg { try JSONDecoder().decode(BundleCfg.self, from: Data(contentsOf: cfgURL)) }
}

func makeConfig(_ b: Bundle) throws -> VZVirtualMachineConfiguration {
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
    n.attachment = VZNATNetworkDeviceAttachment()
    n.macAddress = VZMACAddress(string: cfg.mac)!
    c.networkDevices = [n]
    c.socketDevices = [VZVirtioSocketDeviceConfiguration()]
    c.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    c.keyboards = [VZUSBKeyboardConfiguration()]
    c.pointingDevices = [VZUSBScreenCoordinatePointingDeviceConfiguration()]
    try c.validate()
    return c
}

final class Delegate: NSObject, VZVirtualMachineDelegate {
    var stopped = false
    func guestDidStop(_ vm: VZVirtualMachine) { log("guest did stop"); stopped = true }
    func virtualMachine(_ vm: VZVirtualMachine, didStopWithError error: Error) { log("vm stopped with error: \(error)"); stopped = true }
}

/// Host listener on 2048: answers "host" and closes. Logs every dial-in.
final class HostListener: NSObject, VZVirtioSocketListenerDelegate {
    func listener(_ listener: VZVirtioSocketListener, shouldAcceptNewConnection c: VZVirtioSocketConnection, from device: VZVirtioSocketDevice) -> Bool {
        result(["event": "guest dialed host", "src": c.sourcePort, "dst": c.destinationPort, "wall": wall()])
        let fd = c.fileDescriptor
        _ = "host\n".withCString { write(fd, $0, 5) }
        c.close()
        return true
    }
}

/// Read one line (up to 256 bytes) from fd with a timeout.
func readLine(_ fd: Int32, timeoutMs: Int32) -> String {
    let cap = 256
    var buf = [UInt8](repeating: 0, count: cap)
    var got = 0
    let deadline = now() + Double(timeoutMs) / 1000
    while got < cap {
        var p = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        let left = Int32(max(0, (deadline - now()) * 1000))
        if poll(&p, 1, left) <= 0 { break }
        let off = got
        let n = buf.withUnsafeMutableBytes { read(fd, $0.baseAddress! + off, cap - off) }
        if n <= 0 { break }
        got += n
        if buf[..<got].contains(10) { break }
    }
    return String(decoding: buf[..<got], as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
}

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let delegate = Delegate()
    let hl = HostListener()
    init(_ b: Bundle) throws {
        vm = VZVirtualMachine(configuration: try makeConfig(b))
        vm.delegate = delegate
    }
    var socket: VZVirtioSocketDevice { vm.socketDevices[0] as! VZVirtioSocketDevice }

    func connect(port: UInt32, timeout: Double) async -> Result<VZVirtioSocketConnection, Error> {
        final class Box { var r: Result<VZVirtioSocketConnection, Error>? }
        let box = Box()
        let start = now()
        socket.connect(toPort: port) { box.r = $0 }
        while box.r == nil && now() - start < timeout { try? await Task.sleep(nanoseconds: 200_000) }
        return box.r ?? .failure(err("connect timeout"))
    }

    /// One attempt: connect and read the banner line.
    func probe(port: UInt32) async -> [String: Any] {
        let t0 = now()
        var r: [String: Any] = ["port": port, "wall": wall()]
        switch await connect(port: port, timeout: 2) {
        case .success(let c):
            let fd = c.fileDescriptor
            r["connectMs"] = (now() - t0) * 1000
            r["src"] = c.sourcePort
            r["banner"] = await Task.detached { readLine(fd, timeoutMs: 1000) }.value
            c.close()
        case .failure(let e):
            r["connectMs"] = (now() - t0) * 1000
            r["error"] = "\(e)"
        }
        return r
    }
}

/// Serve the control socket on a background thread; VM calls hop to main.
func serveControl(path: String, m: Machine) {
    unlink(path)
    let s = socket(AF_UNIX, SOCK_STREAM, 0)
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    withUnsafeMutableBytes(of: &addr.sun_path) { p in for (i, b) in bytes.enumerated() { p[i] = b } }
    addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
    let rc = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(s, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
    precondition(rc == 0, "bind ctl errno \(errno)")
    precondition(listen(s, 4) == 0)
    Thread.detachNewThread {
        while true {
            let c = accept(s, nil, nil)
            if c < 0 { continue }
            Thread.detachNewThread { handle(c, m) }
        }
    }
}

func handle(_ c: Int32, _ m: Machine) {
    let fh = FileHandle(fileDescriptor: c, closeOnDealloc: true)
    func reply(_ kv: [String: Any]) {
        let line = json(kv) + "\n"
        fh.write(line.data(using: .utf8)!)
        result(["ctl": kv])
    }
    var pending = Data()
    while true {
        let d = fh.availableData
        if d.isEmpty { return }
        pending.append(d)
        while let nl = pending.firstIndex(of: 10) {
            let line = String(decoding: pending[..<nl], as: UTF8.self)
            pending.removeSubrange(...nl)
            let f = line.split(separator: " ").map(String.init)
            guard let cmd = f.first else { continue }
            let sem = DispatchSemaphore(value: 0)
            switch cmd {
            case "connect":
                let port = UInt32(f[1])!
                Task { @MainActor in reply(["cmd": "connect"].merging(await m.probe(port: port)) { a, _ in a }); sem.signal() }
                sem.wait()
            case "poll":
                let port = UInt32(f[1])!, iv = Double(f[2])! / 1000, dur = Double(f[3])! / 1000
                Task { @MainActor in
                    let end = now() + dur
                    var i = 0
                    while now() < end {
                        let t = now()
                        reply(["cmd": "poll", "i": i].merging(await m.probe(port: port)) { a, _ in a })
                        i += 1
                        let wait = iv - (now() - t)
                        if wait > 0 { try? await Task.sleep(nanoseconds: UInt64(wait * 1e9)) }
                    }
                    reply(["cmd": "poll", "done": true])
                    sem.signal()
                }
                sem.wait()
            case "stop":
                reply(["cmd": "stop"])
                Task { @MainActor in
                    if m.vm.canStop { try? await m.vm.stop() }
                    exit(0)
                }
                return
            default:
                reply(["error": "unknown command \(cmd)"])
            }
        }
    }
}

@MainActor
func run(_ b: Bundle, ctl: String, minutes: Double, user: String?, pass: String?) async throws {
    let m = try Machine(b)
    let vl = VZVirtioSocketListener()
    vl.delegate = m.hl
    m.socket.setSocketListener(vl, forPort: 2048)
    let opts = VZMacOSVirtualMachineStartOptions()
    if let user, let pass {
        let p = VZMacGuestProvisioningOptions()
        p.fullName = "x27 spike"
        p.username = user
        p.password = pass
        p.logsInAutomatically = false
        p.enablesRemoteLogin = true
        try opts.setGuestProvisioning(p)
    }
    let t0 = now()
    try await m.vm.start(options: opts)
    result(["event": "started", "seconds": now() - t0, "mac": try b.cfg().mac, "provision": user != nil])
    serveControl(path: ctl, m: m)
    let deadline = now() + minutes * 60
    while now() < deadline && !m.delegate.stopped && m.vm.state != .stopped {
        try? await Task.sleep(nanoseconds: 200_000_000)
    }
    result(["event": m.delegate.stopped || m.vm.state == .stopped ? "guest stopped" : "timeout", "seconds": now() - t0])
    if m.vm.canStop { try? await m.vm.stop() }
}

let args = CommandLine.arguments
Task { @MainActor in
    do {
        switch args.count > 1 ? args[1] : "" {
        case "run":
            try await run(Bundle(args[2]), ctl: args[3], minutes: Double(args[4])!,
                          user: args.count > 6 ? args[5] : nil, pass: args.count > 6 ? args[6] : nil)
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
