// swift-tools-version: 5.9
// X03-network-path spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x03",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x03", path: "Sources/x03")
    ]
)
