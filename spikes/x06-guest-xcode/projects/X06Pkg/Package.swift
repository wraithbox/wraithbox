// swift-tools-version: 6.0
// X06-guest-xcode: a SwiftPM package with a resource bundle. Throwaway.
import PackageDescription

let package = Package(
    name: "X06Pkg",
    platforms: [.macOS(.v15), .iOS(.v18)],
    products: [.library(name: "X06Pkg", targets: ["X06Pkg"])],
    targets: [
        .target(name: "X06Pkg", resources: [.process("Resources")]),
        .testTarget(name: "X06PkgTests", dependencies: ["X06Pkg"]),
    ]
)
