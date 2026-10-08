import SwiftUI

enum Greeting {
    static let text = "hello from x06"
}

@main
struct MacApp: App {
    var body: some Scene {
        WindowGroup { Text(Greeting.text) }
    }
}
