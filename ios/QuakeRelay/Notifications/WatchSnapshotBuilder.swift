import Foundation
import SwiftData

@MainActor
enum WatchSnapshotBuilder {
    // Application context must stay below WatchConnectivity's message limit.
    static let byteBudget = 48_000
    static let historyPerCategory = 15

    static func make(context: ModelContext, paired: Bool, lastSyncAt: Date?, receiverSummary: String?, now: Date = Date()) throws -> WatchSnapshot {
        guard paired else {
            return WatchSnapshot(generatedAt: now, lastSyncAt: nil,
                receiverSummary: nil, events: [], isPaired: false)
        }
        let events = try context.fetch(FetchDescriptor<EventEntity>()).filter { WatchEventCategory(rawValue: $0.category) != nil }
            .sorted { $0.latestReportAt != $1.latestReportAt ? $0.latestReportAt > $1.latestReportAt : $0.id < $1.id }
        let reports = try context.fetch(FetchDescriptor<ReportEntity>())
        let index = EventContinuationIndex(reports: reports)
        var counts: [String: Int] = [:]
        var selected: [WatchEventSnapshot] = []
        for event in events {
            let continuation = index.status(for: event, now: now)
            let count = counts[event.category, default: 0]
            guard continuation.isOngoing || count < historyPerCategory else { continue }
            if !continuation.isOngoing { counts[event.category] = count + 1 }
            let eventReports = index.reportsByEvent[event.id] ?? []
            let activeReport = eventReports.filter { continuation.reportIDs.contains($0.id) }
                .max(by: ReportPublicationOrder.areInAscendingOrder)
            let latestReport = activeReport ?? eventReports.first { $0.serverSequence == Int64(event.latestRevision ?? -1) }
            let tsunami = TsunamiPublication.latest(in: eventReports, eventID: event.id)
            let publication = tsunami.flatMap { TsunamiPublication(report: $0).status } ?? event.publicationLabel
            let stateText = continuation.state == .unknown ? "継続状況未確認" :
                continuation.state == .ended ? continuation.explanation : publication
            selected.append(WatchEventSnapshot(id: event.id, category: WatchEventCategory(rawValue: event.category)!,
                title: String((event.category == "earthquake" ? event.numericHypocenter.epicenter ?? event.displayTitle : event.displayTitle).prefix(80)),
                summary: String((latestReport?.body ?? "").prefix(240)), occurredAt: event.originTime,
                updatedAt: event.latestReportAt, publicationStatus: String(stateText.prefix(120)),
                isOngoing: continuation.isOngoing, ongoingReason: String(continuation.explanation.prefix(160)),
                ongoingUntil: continuation.until))
        }
        var snapshot = WatchSnapshot(generatedAt: now, lastSyncAt: lastSyncAt,
            receiverSummary: receiverSummary, events: selected)
        // Retain the newest history of both categories as well as every ongoing
        // publication, even when one category's events are much older.
        var protectedHistory: Set<String> = []
        for category in WatchEventCategory.allCases {
            if let newest = snapshot.events.first(where: { $0.category == category && !$0.isOngoing }) {
                protectedHistory.insert(newest.id)
            }
        }
        while try snapshot.encoded().count > byteBudget,
              let historical = snapshot.events.lastIndex(where: { !$0.isOngoing && !protectedHistory.contains($0.id) }) {
            snapshot.events.remove(at: historical)
        }
        if try snapshot.encoded().count > byteBudget {
            for i in snapshot.events.indices {
                snapshot.events[i].summary = ""
                snapshot.events[i].ongoingReason = nil
                snapshot.events[i].title = String(snapshot.events[i].title.prefix(40))
            }
        }
        guard try snapshot.encoded().count <= byteBudget else {
            throw CocoaError(.fileWriteOutOfSpace)
        }
        return snapshot
    }
}
