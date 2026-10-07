import XCTest
@testable import QuakeRelay

final class DeepLinkTests: XCTestCase {
    func testParsesEventAndReportDeepLink() throws {
        let url = try XCTUnwrap(URL(string: "quake-relay://event/evt_ABC123?report=rpt_DEF456"))
        XCTAssertEqual(
            DeepLinkParser.parse(url: url),
            .event(id: "evt_ABC123", reportID: "rpt_DEF456")
        )
    }

    func testRejectsWrongSchemeAndMissingEvent() throws {
        XCTAssertNil(DeepLinkParser.parse(url: try XCTUnwrap(URL(string: "https://example.jp/event/evt"))))
        XCTAssertNil(DeepLinkParser.parse(url: try XCTUnwrap(URL(string: "quake-relay://event"))))
    }

    func testParsesPushPayload() {
        let payload: [AnyHashable: Any] = [
            "event_id": "evt_001",
            "report_id": "rpt_004"
        ]
        XCTAssertEqual(
            DeepLinkParser.parse(userInfo: payload),
            .event(id: "evt_001", reportID: "rpt_004")
        )
    }
}

extension DeepLinkTests {
    @MainActor
    func testRoutesToProductTabAndResetsAllPaths() {
        let router = DeepLinkRouter()
        router.open(.event(id: "tsunami-example", reportID: "abc"))
        XCTAssertEqual(router.selectedTab, .tsunami)
        XCTAssertEqual(router.tsunamiPath, [.event(id: "tsunami-example", reportID: "abc")])
        router.open(.event(id: "advisory-example", reportID: nil))
        XCTAssertEqual(router.selectedTab, .advisory)
        router.open(.event(id: "20261005000000", reportID: nil))
        XCTAssertEqual(router.selectedTab, .events)
        router.reset()
        XCTAssertTrue(router.path.isEmpty && router.tsunamiPath.isEmpty && router.advisoryPath.isEmpty)
    }
}
