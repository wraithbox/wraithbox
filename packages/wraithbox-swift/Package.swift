// swift-tools-version: 6.4
// macOS-native components. wb-vmd runs VMs with the Virtualization framework
// for the cross-platform Go daemon wb-hostd; it holds no policy and no
// secrets. Only code that needs Apple frameworks belongs here.
// See docs/spec/S10-tech-stack/S10-00-tech-stack.md and docs/spec/S12-platforms/S12-00-platforms.md.
import PackageDescription

let package = Package(
    name: "wraithbox-swift",
    // macOS 27 is the oldest v1 host (NFR04-host-platforms), but the
    // package targets macOS 26, so that its build and tests run on a Mac
    // that can't have 27 yet (S02-toolchain). The programs refuse an older
    // host at run time (HostRequirement), and code that calls an API new
    // in macOS 27 checks it with #available, which the compiler enforces.
    // `.v26` needs tools version 6.2; the package declares 6.4 for Xcode 27.
    // The app bundle doesn't take this target: scripts/app-bundle.sh
    // builds it for macOS 27.
    platforms: [
        .macOS(.v26)
    ],
    products: [
        .library(name: "WraithBoxVM", targets: ["WraithBoxVM"]),
        .library(name: "WraithBoxRPC", targets: ["WraithBoxRPC"]),
        .library(name: "WraithBoxVMD", targets: ["WraithBoxVMD"]),
        .executable(name: "wb-vmd", targets: ["wb-vmd"]),
        // Dev-only test tooling, never in the app bundle: makes, clones,
        // provisions, stops and removes test VM bundles in the spike cache
        // through `mise run vm:*` (S10-tech-stack, "Test tooling").
        .executable(name: "wb-dev-bundle", targets: ["wb-dev-bundle"]),
    ],
    // Exact versions: Package.resolved pins the rest of the tree, and
    // dependabot proposes the bumps (AGENTS.md, "Supply chain"). The two
    // protoc plugins that `mise run proto:generate-swift` runs,
    // protoc-gen-swift and protoc-gen-grpc-swift-2, are built from these
    // same packages (B52-proto-contracts).
    dependencies: [
        .package(url: "https://github.com/grpc/grpc-swift-2.git", exact: "2.4.4"),
        .package(url: "https://github.com/grpc/grpc-swift-nio-transport.git", exact: "2.11.0"),
        .package(url: "https://github.com/grpc/grpc-swift-protobuf.git", exact: "2.5.0"),
        .package(url: "https://github.com/apple/swift-protobuf.git", exact: "1.39.0"),
        .package(url: "https://github.com/apple/swift-nio.git", exact: "2.104.0"),
    ],
    targets: [
        .target(name: "WraithBoxVM"),
        // Generated code only, from proto/wraithbox/vmd/v1/vmd.proto by
        // `mise run proto:generate-swift`. Never edited by hand:
        // `proto:check-swift` fails on any difference (B52-proto-contracts).
        .target(
            name: "WraithBoxRPC",
            dependencies: [
                .product(name: "GRPCCore", package: "grpc-swift-2"),
                .product(name: "GRPCProtobuf", package: "grpc-swift-protobuf"),
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ]
        ),
        // wb-vmd's daemon: confinement, the gRPC server on its inherited
        // socket, and the VM (S04-03-self-sandboxing, S04-04-vm-processes).
        // A library, so that tests reach it; the executable only calls it.
        .target(
            name: "WraithBoxVMD",
            dependencies: [
                "WraithBoxVM",
                "WraithBoxRPC",
                .product(name: "GRPCCore", package: "grpc-swift-2"),
                .product(name: "GRPCProtobuf", package: "grpc-swift-protobuf"),
                .product(name: "GRPCNIOTransportHTTP2Posix", package: "grpc-swift-nio-transport"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio"),
            ]
        ),
        .executableTarget(
            name: "wb-vmd",
            dependencies: ["WraithBoxVMD"]
        ),
        // An executable target that only its tests depend on. Its one
        // file with a NAT network is excepted from the static check of
        // S04-02-vm-provider (.swiftlint.yml).
        .executableTarget(
            name: "wb-dev-bundle",
            dependencies: ["WraithBoxVM"]
        ),
        .testTarget(
            name: "WbDevBundleTests",
            dependencies: ["wb-dev-bundle"]
        ),
        .testTarget(
            name: "WraithBoxVMTests",
            dependencies: ["WraithBoxVM"]
        ),
        .testTarget(
            name: "WraithBoxVMDTests",
            dependencies: [
                "WraithBoxVMD",
                "WraithBoxVM",
                "WraithBoxRPC",
                .product(name: "GRPCCore", package: "grpc-swift-2"),
                .product(name: "GRPCInProcessTransport", package: "grpc-swift-2"),
                .product(name: "GRPCProtobuf", package: "grpc-swift-protobuf"),
                .product(name: "GRPCNIOTransportHTTP2Posix", package: "grpc-swift-nio-transport"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio"),
            ]
        ),
        .testTarget(
            name: "WraithBoxRPCTests",
            dependencies: [
                "WraithBoxRPC",
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ]
        ),
    ]
)
