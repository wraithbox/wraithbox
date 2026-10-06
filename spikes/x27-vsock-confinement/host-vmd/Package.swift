// swift-tools-version: 5.9
// X27-vsock-confinement spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x27",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x27", path: "Sources/x27")
    ]
)
