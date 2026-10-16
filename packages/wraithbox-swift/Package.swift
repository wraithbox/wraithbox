// swift-tools-version: 6.2
// macOS-native components. wb-vmd runs VMs with the Virtualization framework
// for the cross-platform Go daemon wb-hostd; it holds no policy and no
// secrets. Only code that needs Apple frameworks belongs here.
// See docs/spec/S10-tech-stack/S10-00-tech-stack.md and docs/spec/S12-platforms/S12-00-platforms.md.
import PackageDescription

let package = Package(
    name: "wraithbox-swift",
    platforms: [
        .macOS(.v15)
    ],
    products: [
        .library(name: "WraithBoxVM", targets: ["WraithBoxVM"]),
        .library(name: "WraithBoxRPC", targets: ["WraithBoxRPC"]),
        .executable(name: "wb-vmd", targets: ["wb-vmd"]),
    ],
    // Exact versions: Package.resolved pins the rest of the tree, and
    // dependabot proposes the bumps (AGENTS.md, "Supply chain"). The two
    // protoc plugins that `mise run proto:generate-swift` runs,
    // protoc-gen-swift and protoc-gen-grpc-swift-2, are built from these
    // same packages (B52-proto-contracts).
    dependencies: [
        .package(url: "https://github.com/grpc/grpc-swift-2.git", exact: "2.4.3"),
        .package(url: "https://github.com/grpc/grpc-swift-protobuf.git", exact: "2.4.1"),
        .package(url: "https://github.com/apple/swift-protobuf.git", exact: "1.38.1"),
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
        .executableTarget(
            name: "wb-vmd",
            dependencies: ["WraithBoxVM"]
        ),
        .testTarget(
            name: "WraithBoxVMTests",
            dependencies: ["WraithBoxVM"]
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
