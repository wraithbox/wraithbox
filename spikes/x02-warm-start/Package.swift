// swift-tools-version: 5.9
// X02-warm-start spike. Throwaway code, not held to the project gates.
import PackageDescription

let package = Package(
    name: "x02",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "x02", path: "Sources/x02")
    ]
)
