// swift-tools-version: 6.2
// macOS-native components. wb-vmd runs VMs with the Virtualization framework
// for the cross-platform Go daemon wb-hostd; it holds no policy and no
// secrets. Only code that needs Apple frameworks belongs here.
// See docs/spec/010-tech-stack.md and docs/spec/012-platforms.md.
import PackageDescription

let package = Package(
    name: "wraithbox-swift",
    platforms: [
        .macOS(.v15)
    ],
    products: [
        .library(name: "WraithBoxVM", targets: ["WraithBoxVM"]),
        .executable(name: "wb-vmd", targets: ["wb-vmd"]),
    ],
    targets: [
        .target(name: "WraithBoxVM"),
        .executableTarget(
            name: "wb-vmd",
            dependencies: ["WraithBoxVM"]
        ),
        .testTarget(
            name: "WraithBoxVMTests",
            dependencies: ["WraithBoxVM"]
        ),
    ]
)
