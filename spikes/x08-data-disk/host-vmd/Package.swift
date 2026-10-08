// swift-tools-version: 5.9
// X08-data-disk spike: a wb-vmd stand-in. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x08",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(name: "x08", path: "Sources/x08")
    ]
)
