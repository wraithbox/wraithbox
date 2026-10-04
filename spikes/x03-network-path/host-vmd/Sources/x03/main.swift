// X03-network-path spike: a wb-vmd stand-in. Throwaway code, not held to the gates.
//
// Boots a macOS guest (an X18-vsock-handoff bundle, cloned) whose only NIC is
// VZFileHandleNetworkDeviceAttachment on a SOCK_DGRAM socket pair. The host
// end goes to x03-netd over a Unix socket with SCM_RIGHTS, and this process
// closes its copy at once (X18-vsock-handoff). No NAT attachment, no shared
// directory, ever.
//
//   run <bundle> <netdSock> <minutes> [--mtu N] [--provision <user> <passFile>]
//       cold boot, hand the NIC to x03-netd, run until the guest stops or
//       <minutes> pass, then stop. --provision: first boot of an installed
//       bundle, creating a user with Remote Login on.
import Darwin
import Foundation
import Virtualization

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

func err(_ s: String) -> NSError { NSError(domain: "x03", code: 1, userInfo: [NSLocalizedDescriptionKey: s]) }

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

/// The socket pair: the guest end goes into the configuration, the host end
/// to x03-netd. Apple's header: SO_RCVBUF at least 2x SO_SNDBUF, 4x recommended.
final class NetPair {
    let hostFD: Int32
    let guestHandle: FileHandle
    init() {
        var fds: [Int32] = [0, 0]
        precondition(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0)
        var snd: Int32 = 1 << 20
        var rcv: Int32 = 4 << 20
        for fd in fds {
            setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &snd, socklen_t(MemoryLayout<Int32>.size))
            setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &rcv, socklen_t(MemoryLayout<Int32>.size))
        }
        hostFD = fds[0]
        guestHandle = FileHandle(fileDescriptor: fds[1], closeOnDealloc: true)
    }
}

func makeConfig(_ b: Bundle, nic: FileHandle, mtu: Int) throws -> VZVirtualMachineConfiguration {
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

    // The only NIC: the file-handle attachment. Never NAT, never bridged.
    let att = VZFileHandleNetworkDeviceAttachment(fileHandle: nic)
    att.maximumTransmissionUnit = mtu
    let n = VZVirtioNetworkDeviceConfiguration()
    n.attachment = att
    n.macAddress = VZMACAddress(string: cfg.mac)!
    c.networkDevices = [n]
    // No directory sharing devices.
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
    func virtualMachine(_ vm: VZVirtualMachine, networkDevice: VZNetworkDevice, attachmentWasDisconnectedWithError error: Error) {
        log("network attachment disconnected: \(error)")
        result(["event": "attachment-disconnected", "error": "\(error)"])
    }
}

/// Send one line with fd attached as SCM_RIGHTS (Darwin cmsghdr: 12 bytes, data aligned to 4).
func sendFD(path: String, fd passFD: Int32, line: String) throws -> Int32 {
    let s = socket(AF_UNIX, SOCK_STREAM, 0)
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { throw err("path too long") }
    withUnsafeMutableBytes(of: &addr.sun_path) { p in for (i, b) in bytes.enumerated() { p[i] = b } }
    addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
    let rc = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(s, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
    guard rc == 0 else { throw err("connect \(path): errno \(errno)") }
    var data = Array((line + "\n").utf8)
    let n: Int = data.withUnsafeMutableBytes { dp in
        var iov = iovec(iov_base: dp.baseAddress, iov_len: dp.count)
        var msg = msghdr()
        var control = [UInt8](repeating: 0, count: 16)
        return control.withUnsafeMutableBytes { cp in
            cp.storeBytes(of: UInt32(16), toByteOffset: 0, as: UInt32.self)
            cp.storeBytes(of: SOL_SOCKET, toByteOffset: 4, as: Int32.self)
            cp.storeBytes(of: SCM_RIGHTS, toByteOffset: 8, as: Int32.self)
            cp.storeBytes(of: passFD, toByteOffset: 12, as: Int32.self)
            msg.msg_control = cp.baseAddress
            msg.msg_controllen = 16
            return withUnsafeMutablePointer(to: &iov) { ip in
                msg.msg_iov = ip
                msg.msg_iovlen = 1
                return sendmsg(s, &msg, 0)
            }
        }
    }
    guard n == data.count else { throw err("sendmsg: \(n) errno \(errno)") }
    return s  // kept open; x03-netd logs when it closes
}

/// Provisioning (macOS 27 VZMacGuestProvisioningOptions) creates a user with
/// Remote Login on the first boot. Unlike X18-vsock-handoff, this boot also
/// has only the file-handle NIC: the spike reaches sshd through x03-netd.
struct Provision {
    var user: String
    var pass: String
}

@MainActor
func run(_ b: Bundle, netd: String, minutes: Double, mtu: Int, provision: Provision? = nil) async throws {
    let pair = NetPair()
    let t0 = now()
    let vm = VZVirtualMachine(configuration: try makeConfig(b, nic: pair.guestHandle, mtu: mtu))
    let d = Delegate()
    vm.delegate = d
    let ctl = try sendFD(path: netd, fd: pair.hostFD, line: "{\"mtu\":\(mtu)}")
    close(pair.hostFD)
    result(["event": "nic-passed", "mtu": mtu, "swiftClosedHostEnd": true])
    if let pv = provision {
        let p = VZMacGuestProvisioningOptions()
        p.fullName = "x03 spike"
        p.username = pv.user
        p.password = pv.pass
        p.logsInAutomatically = false
        p.enablesRemoteLogin = true
        let opts = VZMacOSVirtualMachineStartOptions()
        try opts.setGuestProvisioning(p)
        try await vm.start(options: opts)
    } else {
        try await vm.start()
    }
    result(["event": "started", "seconds": now() - t0])
    let deadline = now() + minutes * 60
    while now() < deadline && !d.stopped && vm.state != .stopped {
        try? await Task.sleep(nanoseconds: 500_000_000)
    }
    result(["event": d.stopped ? "guest-stopped" : "timeout", "seconds": now() - t0])
    if vm.canStop { try? await vm.stop() }
    close(ctl)
}

let args = CommandLine.arguments
Task { @MainActor in
    do {
        guard args.count >= 5, args[1] == "run" else {
            log("usage: x03 run <bundle> <netdSock> <minutes> [--mtu N]")
            exit(2)
        }
        var mtu = 1500
        if let i = args.firstIndex(of: "--mtu"), i + 1 < args.count { mtu = Int(args[i + 1])! }
        var pv: Provision? = nil
        if let i = args.firstIndex(of: "--provision"), i + 2 < args.count {
            let pass = try String(contentsOfFile: args[i + 2], encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
            pv = Provision(user: args[i + 1], pass: pass)
        }
        try await run(Bundle(args[2]), netd: args[3], minutes: Double(args[4])!, mtu: mtu, provision: pv)
        exit(0)
    } catch {
        log("error: \(error)")
        result(["error": "\(error)"])
        exit(1)
    }
}
RunLoop.main.run()
