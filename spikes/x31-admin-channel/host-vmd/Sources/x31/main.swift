// X31-admin-channel spike: a wb-vmd / wb-hostd stand-in. Throwaway code, not
// held to the gates. Based on the X27-vsock-confinement harness (itself from
// X18-vsock-handoff).
//
// Boots a test bundle that `mise run vm:install` made and `vm:provision`
// provisioned (the file names wb-vmd opens, S06-05-disks), with a NAT network
// so the spike can reach the guest over SSH, a vsock device, and optionally a
// guest tools disk image attached read-only as the third block device, as
// wb-vmd attaches it (S06-12-guest-agent, "Guest tools disk"). The host
// registers no vsock listener: as in the product, the host opens every
// connection.
//
// A control socket (Unix stream, one JSON reply line per request line):
//   connect <port>                    connect to the guest port, read one line
//   poll <port> <intervalMs> <durMs>  connect every interval for dur, one reply
//                                     line per attempt, then {"done":true}
//   relay <port>                      connect, reply one JSON line, then relay
//                                     raw bytes both ways until either end closes
//   stop                              stop the VM and exit
//
// Commands:
//   run <bundle> <ctlSock> <minutes> <mac> [<tools.img>]
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

func err(_ s: String) -> NSError { NSError(domain: "x31", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

struct BundleCfg: Codable {
    var cpuCount: Int
    var memoryGiB: Int
}

final class Bundle {
    let dir: URL
    init(_ path: String) { dir = URL(fileURLWithPath: path) }
    var aux: URL { dir.appendingPathComponent("aux.wbstore") }
    var hw: URL { dir.appendingPathComponent("hw.bin") }
    var mid: URL { dir.appendingPathComponent("mid.bin") }
    var sys: URL { dir.appendingPathComponent("sys.wbdisk") }
    var data: URL { dir.appendingPathComponent("data.wbdisk") }
    var cfgURL: URL { dir.appendingPathComponent("dev-bundle.json") }
    func cfg() throws -> BundleCfg { try JSONDecoder().decode(BundleCfg.self, from: Data(contentsOf: cfgURL)) }
}

func makeConfig(_ b: Bundle, mac: String, tools: String?) throws -> VZVirtualMachineConfiguration {
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
    c.cpuCount = cfg.cpuCount
    c.memorySize = UInt64(cfg.memoryGiB) << 30
    let gfx = VZMacGraphicsDeviceConfiguration()
    gfx.displays = [VZMacGraphicsDisplayConfiguration(widthInPixels: 1280, heightInPixels: 800, pixelsPerInch: 80)]
    c.graphicsDevices = [gfx]
    func disk(_ u: URL, ro: Bool = false) throws -> VZVirtioBlockDeviceConfiguration {
        let a = try VZDiskImageStorageDeviceAttachment(url: u, readOnly: ro, cachingMode: .automatic, synchronizationMode: .full)
        return VZVirtioBlockDeviceConfiguration(attachment: a)
    }
    c.storageDevices = [try disk(b.sys), try disk(b.data)]
    if let tools { c.storageDevices.append(try disk(URL(fileURLWithPath: tools), ro: true)) }
    let n = VZVirtioNetworkDeviceConfiguration()
    n.attachment = VZNATNetworkDeviceAttachment()
    n.macAddress = VZMACAddress(string: mac)!
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
    init(_ b: Bundle, mac: String, tools: String?) throws {
        vm = VZVirtualMachine(configuration: try makeConfig(b, mac: mac, tools: tools))
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
            case "relay":
                let port = UInt32(f[1])!
                final class Box { var r: Result<VZVirtioSocketConnection, Error>? }
                let box = Box()
                Task { @MainActor in box.r = await m.connect(port: port, timeout: 5); sem.signal() }
                sem.wait()
                switch box.r! {
                case .success(let vc):
                    reply(["cmd": "relay", "port": port, "src": vc.sourcePort, "wall": wall()])
                    let vfd = vc.fileDescriptor
                    if !pending.isEmpty { pending.withUnsafeBytes { _ = write(vfd, $0.baseAddress!, $0.count) } }
                    splice(c, vfd)
                    result(["event": "relay ended", "port": port, "wall": wall()])
                    vc.close()
                case .failure(let e):
                    reply(["cmd": "relay", "port": port, "error": "\(e)", "wall": wall()])
                }
                return
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

/// Copies bytes both ways between a and b until either end reaches EOF or fails.
func splice(_ a: Int32, _ b: Int32) {
    var buf = [UInt8](repeating: 0, count: 65536)
    var fds = [pollfd(fd: a, events: Int16(POLLIN), revents: 0), pollfd(fd: b, events: Int16(POLLIN), revents: 0)]
    while true {
        if poll(&fds, 2, -1) <= 0 { return }
        for i in 0..<2 where fds[i].revents != 0 {
            let src = fds[i].fd, dst = i == 0 ? b : a
            let n = buf.withUnsafeMutableBytes { read(src, $0.baseAddress!, 65536) }
            if n <= 0 { return }
            var off = 0
            while off < n {
                let w = buf.withUnsafeBytes { write(dst, $0.baseAddress! + off, n - off) }
                if w <= 0 { return }
                off += w
            }
        }
    }
}

@MainActor
func run(_ b: Bundle, ctl: String, minutes: Double, mac: String, tools: String?) async throws {
    let m = try Machine(b, mac: mac, tools: tools)
    let opts = VZMacOSVirtualMachineStartOptions()
    let t0 = now()
    try await m.vm.start(options: opts)
    result(["event": "started", "seconds": now() - t0, "mac": mac, "tools": tools ?? ""])
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
                          mac: args[5], tools: args.count > 6 ? args[6] : nil)
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
