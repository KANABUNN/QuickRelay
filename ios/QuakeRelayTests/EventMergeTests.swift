import XCTest
import SwiftData
@testable import QuakeRelay

@MainActor
final class EventMergeTests: XCTestCase {
    func testNumericQualificationSurvivesPersistenceAndClearsWithNewerSource() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        var dto = TestFixtures.event(eventType: "eew_forecast", magnitude: nil, latestRevision: 8)
        dto.hypocenter = HypocenterDTO(status: "assumed", epicenter: "合成震源", depthKm: 10, magnitude: 1)
        let entity = EventEntity(dto: dto)
        container.mainContext.insert(entity)
        try container.mainContext.save()
        let fetched = try XCTUnwrap(container.mainContext.fetch(FetchDescriptor<EventEntity>()).first)
        XCTAssertTrue(fetched.numericHypocenter.isAssumed)
        XCTAssertEqual(fetched.numericHypocenter.magnitude, 1)
        XCTAssertEqual(fetched.intensityLabel, "予想最大震度")
        fetched.merge(TestFixtures.event(latestRevision: 7))
        XCTAssertTrue(fetched.numericHypocenter.isAssumed)
        fetched.merge(TestFixtures.event(eventType: "earthquake_info", magnitude: 6.2, latestRevision: 9))
        XCTAssertFalse(fetched.numericHypocenter.isAssumed)
        XCTAssertNil(fetched.hypocenterData)
        XCTAssertEqual(fetched.numericHypocenter.magnitude, 6.2)
        XCTAssertEqual(fetched.intensityLabel, "観測最大震度")
    }

    func testLaterIndependentProductDoesNotInheritCancellation() {
        let entity = EventEntity(dto: TestFixtures.event(eventType: "eew_cancel", latestRevision: 5, isCancelled: true))
        entity.merge(TestFixtures.event(eventType: "earthquake_info", latestRevision: 6))
        XCTAssertFalse(entity.isCancelled)
        XCTAssertEqual(entity.eventType, "earthquake_info")
    }

    func testOlderRevisionCannotOverwriteLatestNumericState() {
        let entity = EventEntity(dto: TestFixtures.event(latestRevision: 4))
        let older = TestFixtures.event(
            magnitude: 5.1,
            maxIntensity: "4",
            latestRevision: 3,
            latestReportAt: "2026-08-13T13:30:03.000Z"
        )

        entity.merge(older)

        XCTAssertEqual(entity.latestRevision, 4)
        XCTAssertEqual(entity.magnitude, 6.2)
        XCTAssertEqual(entity.maxIntensity, "5強")
    }

    func testNewServerVersionCancellationReplacesLatestState() {
        let entity = EventEntity(dto: TestFixtures.event(latestRevision: 4))
        let cancellation = TestFixtures.event(
            eventType: "eew_cancel",
            magnitude: nil,
            maxIntensity: nil,
            latestRevision: 5,
            isCancelled: true,
            latestReportAt: "2026-08-13T13:30:06.000Z"
        )

        entity.merge(cancellation)

        XCTAssertTrue(entity.isCancelled)
        XCTAssertEqual(entity.eventType, "eew_cancel")
        XCTAssertNil(entity.magnitude)
        XCTAssertNil(entity.maxIntensity)
        XCTAssertEqual(
            entity.latestReportAt,
            ServerDateParser.parse("2026-08-13T13:30:06.000Z")
        )
    }

    func testRevisionlessEventsUseLatestReportTime() {
        let entity = EventEntity(dto: TestFixtures.event(latestRevision: nil))
        let newer = TestFixtures.event(
            magnitude: 6.4,
            latestRevision: nil,
            latestReportAt: "2026-08-13T13:31:05.000Z"
        )

        entity.merge(newer)
        XCTAssertEqual(entity.magnitude, 6.4)
    }

    func testOlderServerVersionCannotClearCancellation() {
        let entity = EventEntity(dto: TestFixtures.event(
            eventType: "eew_cancel",
            latestRevision: 4,
            isCancelled: true
        ))
        let lateWarning = TestFixtures.event(
            eventType: "eew_warning",
            magnitude: 6.3,
            latestRevision: 3,
            isCancelled: false,
            latestReportAt: "2026-08-13T13:31:05.000Z"
        )

        entity.merge(lateWarning)

        XCTAssertTrue(entity.isCancelled)
        XCTAssertEqual(entity.eventType, "eew_cancel")
    }
}
