// X17-image-build spike: a wb-vmd stand-in. Throwaway code, not held to the gates.
//
// Installs macOS from a restore image into a bundle, and boots a bundle with
// no network that reaches anything, then talks to the test guest daemon
// (x17-guestd) over vsock. The files that make x17-guestd run are written to
// the guest's Data volume from the host by inject.sh, not by this tool.
//
// Bundle layout (a directory):
//   aux.img hw.bin mid.bin sys.img cfg.json
//
// Commands:
//   install <bundle> <ipsw>
//       create a bundle and install macOS
//   boot <bundle> <requests.jsonl|-> [--timeout <sec>] [--provision <user> <password file> <remoteLogin 0|1> <autoLogin 0|1>]
//       cold boot; connect to vsock port 1000 until it answers or <sec> pass;
//       send each request line, print each answer as one JSON line; if a
//       request was {"op":"shutdown"}, wait for the guest to stop.
//       --provision adds VZMacGuestProvisioningOptions (first boot only).
import Darwin
import Foundation
import Virtualization

let guestPort: UInt32 = 1000

func now() -> Double { Double(clock_gettime_nsec_np(CLOCK_UPTIME_RAW)) / 1e9 }

func log(_ s: String) {
    let ts = ISO8601DateFormatter().string(from: Date())
    FileHandle.standardError.write("[\(ts)] \(s)\n".data(using: .utf8)!)
}

func result(_ kv: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: kv, options: [.sortedKeys])
    print(String(data: data, encoding: .utf8)!)
    fflush(stdout)
}

func err(_ s: String) -> NSError { NSError(domain: "x17", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

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
    var cfgURL: URL { dir.appendingPathComponent("cfg.json") }
    func cfg() throws -> BundleCfg { try JSONDecoder().decode(BundleCfg.self, from: Data(contentsOf: cfgURL)) }
}

func createSparse(_ u: URL, gib: Int) throws {
    let fd = open(u.path, O_RDWR | O_CREAT | O_EXCL, 0o644)
    guard fd >= 0 else { throw err("create \(u.path): errno \(errno)") }
    defer { close(fd) }
    guard ftruncate(fd, off_t(gib) << 30) == 0 else { throw err("ftruncate errno \(errno)") }
}

/// The guest end of an unconnected SOCK_DGRAM pair: a network device with
/// nothing behind it, as a product VM has before wb-netd gets the host end.
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

func makeConfig(_ b: Bundle, nic: VZNetworkDeviceAttachment) throws -> VZVirtualMachineConfiguration {
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

    let a = try VZDiskImageStorageDeviceAttachment(url: b.sys, readOnly: false, cachingMode: .automatic, synchronizationMode: .full)
    c.storageDevices = [VZVirtioBlockDeviceConfiguration(attachment: a)]

    let n = VZVirtioNetworkDeviceConfiguration()
    n.attachment = nic
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

@MainActor
final class Machine {
    let vm: VZVirtualMachine
    let delegate = Delegate()
    let net: NetPair?

    init(_ b: Bundle, nat: Bool = false) throws {
        if nat {
            net = nil
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: VZNATNetworkDeviceAttachment()))
        } else {
            let p = NetPair()
            net = p
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: VZFileHandleNetworkDeviceAttachment(fileHandle: p.guestHandle)))
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

    func connectUntil(port: UInt32, deadline: Double) async -> (VZVirtioSocketConnection, Double, Int)? {
        var tries = 0
        while now() < deadline {
            if delegate.stopped { return nil }
            tries += 1
            if case .success(let c) = await connect(port: port, timeout: 1) { return (c, now(), tries) }
            try? await Task.sleep(nanoseconds: 50_000_000)
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

/// Line I/O on a vsock connection's descriptor (blocking, off the main actor).
final class LineConn: @unchecked Sendable {
    let fd: Int32
    private var buf: [UInt8] = []
    init(fd: Int32) { self.fd = fd }

    func writeLine(_ s: String) throws {
        var bytes = Array((s + "\n").utf8)
        var off = 0
        while off < bytes.count {
            let n = bytes.withUnsafeMutableBytes { write(fd, $0.baseAddress! + off, $0.count - off) }
            if n < 0 { if errno == EINTR || errno == EAGAIN { usleep(1000); continue }; throw err("write errno \(errno)") }
            off += n
        }
    }

    func readLine() throws -> String {
        var chunk = [UInt8](repeating: 0, count: 65536)
        while true {
            if let i = buf.firstIndex(of: 10) {
                let line = String(decoding: buf[..<i], as: UTF8.self)
                buf.removeSubrange(...i)
                return line
            }
            let n = chunk.withUnsafeMutableBytes { read(fd, $0.baseAddress!, $0.count) }
            if n < 0 { if errno == EINTR || errno == EAGAIN { usleep(1000); continue }; throw err("read errno \(errno)") }
            if n == 0 { throw err("EOF") }
            buf.append(contentsOf: chunk[0..<n])
        }
    }
}

@MainActor
func install(_ b: Bundle, ipsw: String) async throws {
    let t0 = now()
    let img = try await VZMacOSRestoreImage.image(from: URL(fileURLWithPath: ipsw))
    guard let req = img.mostFeaturefulSupportedConfiguration else { throw err("restore image not supported on this host") }
    log("image \(img.buildVersion)")
    try FileManager.default.createDirectory(at: b.dir, withIntermediateDirectories: true)
    try req.hardwareModel.dataRepresentation.write(to: b.hw)
    try VZMacMachineIdentifier().dataRepresentation.write(to: b.mid)
    _ = try VZMacAuxiliaryStorage(creatingStorageAt: b.aux, hardwareModel: req.hardwareModel, options: [])
    try createSparse(b.sys, gib: 64)
    let cfg = BundleCfg(cpus: max(4, req.minimumSupportedCPUCount), memGiB: 4, mac: VZMACAddress.randomLocallyAdministered().string)
    try JSONEncoder().encode(cfg).write(to: b.cfgURL)
    let m = try Machine(b)
    let inst = VZMacOSInstaller(virtualMachine: m.vm, restoringFromImageAt: URL(fileURLWithPath: ipsw))
    var lastPct = -1
    let obs = inst.progress.observe(\.fractionCompleted, options: [.new]) { p, _ in
        let pct = Int(p.fractionCompleted * 100)
        if pct / 10 != lastPct / 10 { log("install \(pct)%"); lastPct = pct }
    }
    let t1 = now()
    try await inst.install()
    obs.invalidate()
    result(["cmd": "install", "seconds": now() - t0, "installSeconds": now() - t1, "build": img.buildVersion, "mac": cfg.mac])
    await m.hardStop()
}

@MainActor
func boot(_ b: Bundle, requests: String, timeout: Double, prov: (String, String, Bool, Bool)?, nat: Bool) async throws {
    var lines: [String] = []
    if requests != "-" {
        lines = try String(contentsOfFile: requests, encoding: .utf8).split(separator: "\n").map(String.init).filter { !$0.isEmpty && !$0.hasPrefix("#") }
    }
    let m = try Machine(b, nat: nat)
    let opts = VZMacOSVirtualMachineStartOptions()
    if let (u, p, rl, al) = prov {
        let o = VZMacGuestProvisioningOptions()
        o.fullName = "x17 provisioning"
        o.username = u
        o.password = try String(contentsOfFile: p, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
        o.logsInAutomatically = al
        o.enablesRemoteLogin = rl
        try opts.setGuestProvisioning(o)
    }
    let t0 = now()
    try await m.vm.start(options: opts)
    result(["cmd": "boot", "event": "started", "seconds": now() - t0, "provisioning": prov != nil, "nat": nat])
    guard let (c, at, tries) = await m.connectUntil(port: guestPort, deadline: t0 + timeout) else {
        result(["cmd": "boot", "event": "no answer", "seconds": now() - t0, "stopped": m.delegate.stopped])
        await m.hardStop()
        return
    }
    result(["cmd": "boot", "event": "connected", "seconds": at - t0, "tries": tries, "port": guestPort])
    let conn = LineConn(fd: c.fileDescriptor)
    var shutdown = false
    for line in lines {
        let rt0 = now()
        let resp: String
        do {
            resp = try await Task.detached { try conn.writeLine(line); return try conn.readLine() }.value
        } catch {
            result(["cmd": "boot", "event": "request failed", "req": line, "error": "\(error)"])
            break
        }
        var r: [String: Any] = ["cmd": "boot", "event": "answer", "ms": (now() - rt0) * 1000, "t": now() - t0]
        r["req"] = (try? JSONSerialization.jsonObject(with: Data(line.utf8))) ?? line
        r["resp"] = (try? JSONSerialization.jsonObject(with: Data(resp.utf8))) ?? resp
        result(r)
        if line.contains("\"shutdown\"") { shutdown = true }
    }
    c.close()
    if shutdown {
        let ok = await m.waitStopped(timeout: 120)
        result(["cmd": "boot", "event": ok ? "guest stopped" : "stop timeout", "seconds": now() - t0])
        if !ok { await m.hardStop() }
    } else {
        await m.hardStop()
        result(["cmd": "boot", "event": "hard stop", "seconds": now() - t0])
    }
}

@MainActor
func main() async {
    let args = CommandLine.arguments
    do {
        switch args.count > 1 ? args[1] : "" {
        case "install": try await install(Bundle(args[2]), ipsw: args[3])
        case "boot":
            var timeout = 120.0
            var prov: (String, String, Bool, Bool)?
            var nat = false
            var i = 4
            while i < args.count {
                switch args[i] {
                case "--timeout": timeout = Double(args[i + 1])!; i += 2
                case "--provision": prov = (args[i + 1], args[i + 2], args[i + 3] == "1", args[i + 4] == "1"); i += 5
                case "--nat": nat = true; i += 1
                default: throw err("unknown flag \(args[i])")
                }
            }
            try await boot(Bundle(args[2]), requests: args[3], timeout: timeout, prov: prov, nat: nat)
        default:
            FileHandle.standardError.write("usage: x17 install <bundle> <ipsw> | boot <bundle> <requests|-> [--timeout s] [--provision u p remoteLogin(0|1) autoLogin(0|1)] [--nat]\n".data(using: .utf8)!)
            exit(2)
        }
    } catch {
        log("error: \(error)")
        result(["error": "\(error)"])
        exit(1)
    }
    exit(0)
}

await main()
