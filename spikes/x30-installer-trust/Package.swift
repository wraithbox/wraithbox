// swift-tools-version: 5.9
// X30-installer-trust spike: the X25 tool, installing from changed restore images. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x30",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x30", path: "Sources/x30")
    ]
)
