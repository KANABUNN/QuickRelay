import Combine
import Foundation
import SwiftData

enum SyncPersistenceError: LocalizedError, Equatable {
    case sequenceOverflow
    case cursorChanged(expected: UInt64, actual: UInt64)
    case regressedCursor(expectedAtLeast: UInt64, received: UInt64)
    case stalledPage
    case invalidItemSequence(UInt64)
    case mismatchedEvent(reportEventID: String, eventID: String)

    var errorDescription: String? {
        switch self {
        case .sequenceOverflow:
            return "server_sequence exceeds the local database range."
        case let .cursorChanged(expected, actual):
            return "Sync cursor changed during the page transaction (expected \(expected), found \(actual))."
        case let .regressedCursor(expected, received):
            return "Server returned a regressed sync cursor (expected at least \(expected), received \(received))."
        case .stalledPage:
            return "Server reported another sync page without advancing the cursor."
        case let .invalidItemSequence(sequence):
            return "Server returned an out-of-window or unordered report sequence: \(sequence)."
        case let .mismatchedEvent(reportEventID, eventID):
            return "Report event \(reportEventID) does not match embedded event \(eventID)."
        }
    }
}

@MainActor
enum SyncPageApplier {
    static func apply(
        _ page: SyncResponse,
        expectedAfter: UInt64,
        to context: ModelContext,
        now: Date = Date()
    ) throws {
        guard page.nextAfterSequence >= expectedAfter else {
            throw SyncPersistenceError.regressedCursor(
                expectedAtLeast: expectedAfter,
                received: page.nextAfterSequence
            )
        }
        guard !page.hasMore || page.nextAfterSequence > expectedAfter else {
            throw SyncPersistenceError.stalledPage
        }
        guard page.latestCommittedSequence >= page.nextAfterSequence else {
            throw SyncPersistenceError.regressedCursor(
                expectedAtLeast: page.nextAfterSequence,
                received: page.latestCommittedSequence
            )
        }

        var previousSequence = expectedAfter
        for item in page.items {
            guard item.report.eventId == item.event.id else {
                throw SyncPersistenceError.mismatchedEvent(
                    reportEventID: item.report.eventId,
                    eventID: item.event.id
                )
            }
            guard
                item.report.serverSequence > previousSequence,
                item.report.serverSequence <= page.nextAfterSequence
            else {
                throw SyncPersistenceError.invalidItemSequence(item.report.serverSequence)
            }
            previousSequence = item.report.serverSequence
        }

        if let finalItemSequence = page.items.last?.report.serverSequence {
            guard finalItemSequence == page.nextAfterSequence else {
                throw SyncPersistenceError.invalidItemSequence(page.nextAfterSequence)
            }
        } else if page.nextAfterSequence != expectedAfter {
            throw SyncPersistenceError.invalidItemSequence(page.nextAfterSequence)
        }

        let expectedCursor = try localSequence(expectedAfter)
        let nextCursor = try localSequence(page.nextAfterSequence)
        let latestCommitted = try localSequence(page.latestCommittedSequence)

        do {
            try context.transaction {
                let cursor = try fetchOrCreateCursor(in: context)
                guard cursor.lastServerSequence == expectedCursor else {
                    throw SyncPersistenceError.cursorChanged(
                        expected: expectedAfter,
                        actual: UInt64(max(0, cursor.lastServerSequence))
                    )
                }

                for item in page.items {
                    try upsert(item, in: context)
                }

                cursor.lastServerSequence = nextCursor
                cursor.latestCommittedSequence = latestCommitted
                cursor.lastSyncAt = now
            }
        } catch {
            context.rollback()
            throw error
        }
    }

    static func bindServer(_ identity: String, in context: ModelContext) throws {
        let cursor = try fetchOrCreateCursor(in: context)
        guard cursor.serverIdentity != identity else { return }
        do {
            try context.transaction {
                try context.delete(model: ReportEntity.self)
                try context.delete(model: EventEntity.self)
                cursor.lastServerSequence = 0
                cursor.latestCommittedSequence = 0
                cursor.lastSyncAt = nil
                cursor.serverIdentity = identity
            }
        } catch {
            context.rollback()
            throw error
        }
    }

    static func fetchOrCreateCursor(in context: ModelContext) throws -> SyncCursorEntity {
        let singletonID = SyncCursorEntity.singletonID
        var descriptor = FetchDescriptor<SyncCursorEntity>(
            predicate: #Predicate { $0.id == singletonID }
        )
        descriptor.fetchLimit = 1
        if let cursor = try context.fetch(descriptor).first {
            return cursor
        }
        let cursor = SyncCursorEntity()
        context.insert(cursor)
        return cursor
    }

    private static func upsert(_ item: SyncItemDTO, in context: ModelContext) throws {
        let eventID = item.event.id
        var eventDescriptor = FetchDescriptor<EventEntity>(
            predicate: #Predicate { $0.id == eventID }
        )
        eventDescriptor.fetchLimit = 1
        if let existing = try context.fetch(eventDescriptor).first {
            existing.merge(item.event)
        } else {
            context.insert(EventEntity(dto: item.event))
        }

        let reportID = item.report.id
        var reportDescriptor = FetchDescriptor<ReportEntity>(
            predicate: #Predicate { $0.id == reportID }
        )
        reportDescriptor.fetchLimit = 1
        let sequence = try localSequence(item.report.serverSequence)
        if let existing = try context.fetch(reportDescriptor).first {
            existing.update(from: item.report, serverSequence: sequence)
        } else {
            context.insert(ReportEntity(dto: item.report, serverSequence: sequence))
        }
    }

    private static func localSequence(_ sequence: UInt64) throws -> Int64 {
        guard sequence <= UInt64(Int64.max) else {
            throw SyncPersistenceError.sequenceOverflow
        }
        return Int64(sequence)
    }
}

@MainActor
final class EventRepository: ObservableObject {
    @Published private(set) var isSyncing = false
    @Published private(set) var lastSyncAt: Date?
    @Published private(set) var lastServerSequence: UInt64 = 0
    @Published private(set) var latestCommittedSequence: UInt64 = 0
    @Published private(set) var lastError: String?
    @Published private(set) var serverIdentity = ""

    private let context: ModelContext
    private let api: APIClient

    init(context: ModelContext, api: APIClient) {
        self.context = context
        self.api = api
        reloadCursorState()
    }

    func stationCatalog() async throws -> StationCatalogResponse {
        try await api.stationCatalog()
    }

    func sourceDocument(reportID: String) async throws -> Data {
        try await api.sourceDocument(reportID: reportID)
    }

    func reloadCursorState() {
        do {
            let cursor = try SyncPageApplier.fetchOrCreateCursor(in: context)
            lastServerSequence = UInt64(max(0, cursor.lastServerSequence))
            latestCommittedSequence = UInt64(max(0, cursor.latestCommittedSequence))
            serverIdentity = cursor.serverIdentity ?? ""
            lastSyncAt = cursor.lastSyncAt
        } catch {
            lastError = error.localizedDescription
        }
    }

    @discardableResult
    func syncAll(pageSize: Int = 200) async -> Bool {
        guard !isSyncing else { return false }
        isSyncing = true
        lastError = nil
        defer { isSyncing = false }

        do {
            try SyncPageApplier.bindServer(try api.serverIdentity(), in: context)
            reloadCursorState()
            var cursor = lastServerSequence
            var pageCount = 0
            repeat {
                pageCount += 1
                guard pageCount <= 10_000 else { throw SyncPersistenceError.stalledPage }

                let page = try await api.sync(after: cursor, limit: pageSize)
                try SyncPageApplier.apply(page, expectedAfter: cursor, to: context)
                cursor = page.nextAfterSequence
                reloadCursorState()

                if !page.hasMore { break }
            } while true
            return true
        } catch {
            lastError = error.localizedDescription
            reloadCursorState()
            return false
        }
    }
}
