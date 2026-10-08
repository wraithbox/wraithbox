import UIKit
import XCTest
@testable import iOSApp

final class iOSAppTests: XCTestCase {
    func testGreeting() {
        XCTAssertEqual(Greeting.text, "hello from x06")
    }

    func testHostedInApp() {
        XCTAssertTrue(Bundle.main.bundlePath.hasSuffix("iOSApp.app"), Bundle.main.bundlePath)
    }
}
