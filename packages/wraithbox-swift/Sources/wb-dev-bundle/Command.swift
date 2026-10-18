// The command line of wb-dev-bundle, which the `mise run vm:*` tasks call
// (I350). Every path argument is checked by SpikeCache before a command
// touches it.
import Foundation

enum Command: Equatable {
    case install(bundle: String, image: String)
    case clone(source: String, target: String)
    case provision(bundle: String, minutes: Int)
    case stop(bundle: String)
    case clean(bundle: String)

    static let usage = """
        usage: wb-dev-bundle install <bundle> <restore image>
               wb-dev-bundle clone <source bundle> <new bundle>
               wb-dev-bundle provision <bundle> [minutes]
               wb-dev-bundle stop <bundle>
               wb-dev-bundle clean <directory>
        Paths are in ~/Library/Caches/wraithbox-spikes/: absolute, or relative to it.
        """

    static func parse(_ arguments: [String]) throws(Refusal) -> Command {
        let name = arguments.first ?? ""
        let rest = Array(arguments.dropFirst())
        switch (name, rest.count) {
        case ("install", 2):
            return .install(bundle: rest[0], image: rest[1])
        case ("clone", 2):
            return .clone(source: rest[0], target: rest[1])
        case ("provision", 1):
            return .provision(bundle: rest[0], minutes: defaultMinutes)
        case ("provision", 2):
            guard let minutes = Int(rest[1]), (1...maximumMinutes).contains(minutes) else {
                throw Refusal(
                    .usage, rest[1], "minutes are a whole number from 1 to \(maximumMinutes)")
            }
            return .provision(bundle: rest[0], minutes: minutes)
        case ("stop", 1):
            return .stop(bundle: rest[0])
        case ("clean", 1):
            return .clean(bundle: rest[0])
        default:
            throw Refusal(
                .usage, arguments.joined(separator: " "), "not a command with its arguments")
        }
    }

    /// Checks the arguments and runs the command. Returns the exit status:
    /// 0 when it is done, 1 on a refusal or a failure, 2 on a usage error.
    @MainActor
    static func run(_ arguments: [String], cache: SpikeCache, log: Log) async -> Int32 {
        do {
            let command = try parse(arguments)
            return try await command.run(cache: cache, log: log)
        } catch let refusal as Refusal {
            log.refused(refusal)
            if refusal.rule == .usage {
                log.write(usage)
                return 2
            }
            return 1
        } catch {
            log.step("failed: \(error)")
            return 1
        }
    }

    @MainActor
    private func run(cache: SpikeCache, log: Log) async throws -> Int32 {
        switch self {
        case .install(let bundle, let image):
            let image = try cache.restoreImage(image)
            try await installBundle(
                try cache.bundle(bundle, for: .create), image: image, cache: cache, log: log)
        case .clone(let source, let target):
            let source = try cache.bundle(source, for: .read)
            let target = try cache.bundle(target, for: .create)
            try cloneBundle(from: source, to: target, cache: cache, log: log)
        case .provision(let bundle, let minutes):
            try await provisionBundle(
                try cache.bundle(bundle, for: .change), minutes: minutes, log: log)
        case .stop(let bundle):
            return await stopBundle(try cache.bundle(bundle, for: .read), log: log)
        case .clean(let bundle):
            try cleanBundle(try cache.bundle(bundle, for: .change), cache: cache, log: log)
        }
        return 0
    }
}
