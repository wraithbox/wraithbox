// swift-tools-version: 5.9
// X18-vsock-handoff spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x18",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x18", path: "Sources/x18")
    ]
)
