import XCTest

final class MacAppUITests: XCTestCase {
    @MainActor
    func testLaunch() {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.staticTexts["hello from x06"].waitForExistence(timeout: 60))
    }
}
