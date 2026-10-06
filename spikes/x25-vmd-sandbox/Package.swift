// swift-tools-version: 5.9
// X25-vmd-sandbox spike: a wb-vmd stand-in that confines itself. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x25",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x25", path: "Sources/x25")
    ]
)
