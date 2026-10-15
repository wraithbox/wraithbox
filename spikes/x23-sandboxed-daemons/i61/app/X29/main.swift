// Throwaway host app for X29-dev-signing. It exists so that Xcode makes a
// development certificate and a provisioning profile for the personal team.
// The helpers next to it in Contents/MacOS do the keychain work.
import Foundation

print("x29 app: \(Bundle.main.bundleIdentifier ?? "?")")
