import SwiftUI

enum Greeting {
    static let text = "hello from x06"
}

@main
struct iOSApp: App {
    var body: some Scene {
        WindowGroup { Text(Greeting.text) }
    }
}
