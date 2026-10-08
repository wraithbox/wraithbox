// swift-tools-version: 5.9
// X06-guest-xcode spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x06",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x06", path: "Sources/x06")
    ]
)
