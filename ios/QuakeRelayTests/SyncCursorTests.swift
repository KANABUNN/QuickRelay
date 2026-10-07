import SwiftData
import XCTest
@testable import QuakeRelay

@MainActor
final class SyncCursorTests: XCTestCase {
    func testServerChangeResetsCachedHistoryAndCursor() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        try SyncPageApplier.bindServer("https://old.example/api/v1", in: context)
        let item = SyncItemDTO(report: TestFixtures.report(), event: TestFixtures.event())
        try SyncPageApplier.apply(TestFixtures.page(items: [item], next: 1), expectedAfter: 0, to: context)
        try SyncPageApplier.bindServer("https://new.example/api/v1", in: context)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<EventEntity>()), 0)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 0)
        XCTAssertEqual(try SyncPageApplier.fetchOrCreateCursor(in: context).lastServerSequence, 0)
    }

    func testPageUpsertAndCursorCommitTogether() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let item = SyncItemDTO(report: TestFixtures.report(), event: TestFixtures.event())
        let page = TestFixtures.page(items: [item], next: 1)

        try SyncPageApplier.apply(page, expectedAfter: 0, to: context)

        XCTAssertEqual(try context.fetchCount(FetchDescriptor<EventEntity>()), 1)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 1)
        XCTAssertEqual(try SyncPageApplier.fetchOrCreateCursor(in: context).lastServerSequence, 1)
    }

    func testDuplicateReportIDIsUpsertedNotInsertedTwice() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let first = SyncItemDTO(
            report: TestFixtures.report(sequence: 1),
            event: TestFixtures.event(latestRevision: 4)
        )
        let duplicate = SyncItemDTO(
            report: TestFixtures.report(sequence: 2),
            event: TestFixtures.event(latestRevision: 4)
        )

        try SyncPageApplier.apply(
            TestFixtures.page(items: [first], next: 1),
            expectedAfter: 0,
            to: context
        )
        try SyncPageApplier.apply(
            TestFixtures.page(items: [duplicate], next: 2),
            expectedAfter: 1,
            to: context
        )

        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 1)
        XCTAssertEqual(try context.fetch(FetchDescriptor<ReportEntity>()).first?.serverSequence, 2)
        XCTAssertEqual(try SyncPageApplier.fetchOrCreateCursor(in: context).lastServerSequence, 2)
    }

    func testCursorMismatchRollsBackUnsavedCursorMutation() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let first = SyncItemDTO(report: TestFixtures.report(), event: TestFixtures.event())
        try SyncPageApplier.apply(TestFixtures.page(items: [first], next: 1), expectedAfter: 0, to: context)

        let cursor = try SyncPageApplier.fetchOrCreateCursor(in: context)
        cursor.lastServerSequence = 99
        let next = SyncItemDTO(
            report: TestFixtures.report(id: "rpt_002", sequence: 2, messageID: "msg_002"),
            event: TestFixtures.event()
        )

        XCTAssertThrowsError(
            try SyncPageApplier.apply(
                TestFixtures.page(items: [next], next: 2),
                expectedAfter: 1,
                to: context
            )
        )
        XCTAssertEqual(try SyncPageApplier.fetchOrCreateCursor(in: context).lastServerSequence, 1)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 1)
    }

    func testHasMoreCannotStallCursor() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let page = TestFixtures.page(items: [], next: 0, hasMore: true, latest: 5)

        XCTAssertThrowsError(
            try SyncPageApplier.apply(page, expectedAfter: 0, to: container.mainContext)
        ) { error in
            XCTAssertEqual(error as? SyncPersistenceError, .stalledPage)
        }
    }

    func testPageCannotAdvancePastLastReturnedItem() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let item = SyncItemDTO(report: TestFixtures.report(sequence: 1), event: TestFixtures.event())
        let page = TestFixtures.page(items: [item], next: 2)

        XCTAssertThrowsError(
            try SyncPageApplier.apply(page, expectedAfter: 0, to: container.mainContext)
        )
        XCTAssertEqual(try container.mainContext.fetchCount(FetchDescriptor<ReportEntity>()), 0)
    }
}
