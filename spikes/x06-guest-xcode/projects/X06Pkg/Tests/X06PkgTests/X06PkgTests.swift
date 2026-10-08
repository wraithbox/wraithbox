import Testing
import XCTest
@testable import X06Pkg

@Test func greetingFromResourceBundle() throws {
    #expect(try X06Pkg.greeting() == "hello from a resource")
}

final class X06PkgXCTests: XCTestCase {
    func testGreeting() throws {
        XCTAssertEqual(try X06Pkg.greeting(), "hello from a resource")
    }
}
