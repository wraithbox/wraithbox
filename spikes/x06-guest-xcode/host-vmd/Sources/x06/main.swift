// X06-guest-xcode spike: a wb-vmd stand-in. Throwaway code, not held to the gates.
//
// Derived from the X17-image-build tool (tag spike-x17-image-build). Boots a
// bundle that X17's pipeline built and sealed (x17-guestd as a root
// LaunchDaemon on vsock port 1000), and bridges a Unix socket on the host to
// that port, so requests can be sent one at a time while the guest runs.
//
// Bundle layout (a directory): aux.img hw.bin mid.bin sys.img cfg.json
//
//   serve <bundle> <unix socket> [--disk <raw image>]... [--nat] [--mem <GiB>] [--cpus <n>]
//       cold boot; wait until vsock port 1000 answers; then, for each client
//       of the Unix socket, open a new vsock connection to port 1000 and copy
//       bytes both ways until the client closes. Exits when the guest stops.
//       --disk adds a read-only virtio block device (Xcode, runtimes, projects).
//       --nat replaces the network with nothing behind it by a NAT network.
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

func err(_ s: String) -> NSError { NSError(domain: "x06", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

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

struct Opts {
    var disks: [String] = []
    var nat = false
    var memGiB: Int?
    var cpus: Int?
}

func makeConfig(_ b: Bundle, nic: VZNetworkDeviceAttachment, o: Opts) throws -> VZVirtualMachineConfiguration {
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
    c.cpuCount = o.cpus ?? cfg.cpus
    c.memorySize = UInt64(o.memGiB ?? cfg.memGiB) << 30

    let gfx = VZMacGraphicsDeviceConfiguration()
    gfx.displays = [VZMacGraphicsDisplayConfiguration(widthInPixels: 1280, heightInPixels: 800, pixelsPerInch: 80)]
    c.graphicsDevices = [gfx]

    var devs: [VZStorageDeviceConfiguration] = []
    let a = try VZDiskImageStorageDeviceAttachment(url: b.sys, readOnly: false, cachingMode: .automatic, synchronizationMode: .full)
    devs.append(VZVirtioBlockDeviceConfiguration(attachment: a))
    for d in o.disks {
        let da = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: d), readOnly: true)
        devs.append(VZVirtioBlockDeviceConfiguration(attachment: da))
    }
    c.storageDevices = devs

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
    var live: [ObjectIdentifier: VZVirtioSocketConnection] = [:]

    init(_ b: Bundle, o: Opts) throws {
        if o.nat {
            net = nil
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: VZNATNetworkDeviceAttachment(), o: o))
        } else {
            let p = NetPair()
            net = p
            vm = VZVirtualMachine(configuration: try makeConfig(b, nic: VZFileHandleNetworkDeviceAttachment(fileHandle: p.guestHandle), o: o))
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

    func hardStop() async {
        if vm.canStop { try? await vm.stop() }
    }
}

/// Copies from one descriptor to another until EOF or an error.
func pump(_ from: Int32, _ to: Int32) {
    var buf = [UInt8](repeating: 0, count: 1 << 16)
    while true {
        let n = buf.withUnsafeMutableBytes { read(from, $0.baseAddress!, $0.count) }
        if n < 0 && (errno == EINTR || errno == EAGAIN) { usleep(1000); continue }
        if n <= 0 { return }
        var off = 0
        while off < n {
            let w = buf.withUnsafeBytes { write(to, $0.baseAddress! + off, n - off) }
            if w < 0 && (errno == EINTR || errno == EAGAIN) { usleep(1000); continue }
            if w <= 0 { return }
            off += w
        }
    }
}

func listenUnix(_ path: String) throws -> Int32 {
    unlink(path)
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw err("socket errno \(errno)") }
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { throw err("socket path too long") }
    withUnsafeMutableBytes(of: &addr.sun_path) { p in for (i, c) in bytes.enumerated() { p[i] = c } }
    let ok = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
    guard ok == 0 else { throw err("bind errno \(errno)") }
    chmod(path, 0o600)
    guard listen(fd, 16) == 0 else { throw err("listen errno \(errno)") }
    return fd
}

@MainActor
func bridge(_ m: Machine, client: Int32) async {
    guard case .success(let vc) = await m.connect(port: guestPort, timeout: 5) else {
        log("bridge: vsock connect failed")
        close(client)
        return
    }
    let id = ObjectIdentifier(vc)
    m.live[id] = vc
    let vfd = vc.fileDescriptor
    Thread.detachNewThread {
        pump(vfd, client)
        close(client)
    }
    Thread.detachNewThread {
        pump(client, vfd)
        // The client is done: drop the vsock connection, which ends the other pump.
        Task { @MainActor in
            vc.close()
            m.live[id] = nil
        }
    }
}

@MainActor
func serve(_ b: Bundle, sock: String, o: Opts) async throws {
    let m = try Machine(b, o: o)
    let t0 = now()
    try await m.vm.start()
    result(["cmd": "serve", "event": "started", "seconds": now() - t0, "nat": o.nat, "disks": o.disks])
    guard let (c, at, tries) = await m.connectUntil(port: guestPort, deadline: t0 + 300) else {
        result(["cmd": "serve", "event": "no answer", "seconds": now() - t0, "stopped": m.delegate.stopped])
        await m.hardStop()
        return
    }
    c.close()
    result(["cmd": "serve", "event": "connected", "seconds": at - t0, "tries": tries])
    let lfd = try listenUnix(sock)
    result(["cmd": "serve", "event": "listening", "socket": sock])
    Thread.detachNewThread {
        while true {
            let cfd = accept(lfd, nil, nil)
            if cfd < 0 { if errno == EINTR { continue }; return }
            Task { @MainActor in await bridge(m, client: cfd) }
        }
    }
    while !m.delegate.stopped && m.vm.state != .stopped {
        try? await Task.sleep(nanoseconds: 200_000_000)
    }
    unlink(sock)
    result(["cmd": "serve", "event": "guest stopped", "seconds": now() - t0])
}

@MainActor
func main() async {
    let args = CommandLine.arguments
    do {
        guard args.count >= 4, args[1] == "serve" else {
            FileHandle.standardError.write("usage: x06 serve <bundle> <socket> [--disk img]... [--nat] [--mem GiB] [--cpus n]\n".data(using: .utf8)!)
            exit(2)
        }
        var o = Opts()
        var i = 4
        while i < args.count {
            switch args[i] {
            case "--disk": o.disks.append(args[i + 1]); i += 2
            case "--nat": o.nat = true; i += 1
            case "--mem": o.memGiB = Int(args[i + 1])!; i += 2
            case "--cpus": o.cpus = Int(args[i + 1])!; i += 2
            default: throw err("unknown flag \(args[i])")
            }
        }
        try await serve(Bundle(args[2]), sock: args[3], o: o)
    } catch {
        log("error: \(error)")
        result(["error": "\(error)"])
        exit(1)
    }
    exit(0)
}

await main()
