// swift-tools-version: 5.9
// X17-image-build spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x04",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x04", path: "Sources/x04")
    ]
)
