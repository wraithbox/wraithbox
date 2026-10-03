// swift-tools-version: 6.2
// The host daemon (wb-hostd) and its library. Everything that needs Apple
// frameworks (Virtualization, XPC, Keychain UI, notifications) lives here;
// network and protocol work lives in the Go module. See docs/spec/010-tech-stack.md.
import PackageDescription

let package = Package(
    name: "wraithbox-swift",
    platforms: [
        .macOS(.v15)
    ],
    products: [
        .library(name: "WraithBoxHost", targets: ["WraithBoxHost"]),
        .executable(name: "wb-hostd", targets: ["wb-hostd"]),
    ],
    targets: [
        .target(name: "WraithBoxHost"),
        .executableTarget(
            name: "wb-hostd",
            dependencies: ["WraithBoxHost"]
        ),
        .testTarget(
            name: "WraithBoxHostTests",
            dependencies: ["WraithBoxHost"]
        ),
    ]
)
