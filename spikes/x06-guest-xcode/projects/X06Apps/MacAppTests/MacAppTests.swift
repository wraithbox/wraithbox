import AppKit
import XCTest
@testable import MacApp

final class MacAppTests: XCTestCase {
    func testGreeting() {
        XCTAssertEqual(Greeting.text, "hello from x06")
    }

    func testHostedInApp() {
        XCTAssertTrue(Bundle.main.bundlePath.hasSuffix("MacApp.app"), Bundle.main.bundlePath)
        XCTAssertNotNil(NSApplication.shared)
    }
}
