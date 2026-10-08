import Foundation

public enum X06Pkg {
    /// Reads greeting.txt from the target's resource bundle.
    public static func greeting() throws -> String {
        guard let url = Bundle.module.url(forResource: "greeting", withExtension: "txt") else {
            throw CocoaError(.fileNoSuchFile)
        }
        return try String(contentsOf: url, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
