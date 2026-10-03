/// Build version shared by the Swift side of Wraith Box.
public enum WraithBoxVersion {
    /// Overridden for release builds.
    public static let version = "0.0.0-dev"

    /// The version line every binary prints for `--version`.
    public static func line(binary: String) -> String {
        "\(binary) (Wraith Box) \(version)"
    }
}
