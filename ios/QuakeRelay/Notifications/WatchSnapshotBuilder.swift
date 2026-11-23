import Foundation
import SwiftData

@MainActor
enum WatchSnapshotBuilder {
    // Application context must stay below WatchConnectivity's message limit.
    static let byteBudget = 48_000
    static let historyPerCategory = 15

    static func make(context: ModelContext, paired: Bool, lastSyncAt: Date?, receiverSummary: String?,
                     tsunamiRegions: [String] = [], now: Date = Date()) throws -> WatchSnapshot {
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
            let observations = TsunamiObservation.groups(in: eventReports).compactMap { group -> WatchTsunamiObservation? in
                guard let point = observationPoint(for: group, regions: tsunamiRegions) else { return nil }
                return WatchTsunamiObservation(source: group.kind.title, name: String(point.name.prefix(45)),
                    issuedAt: group.report.pressTime ?? group.report.occurredAt ?? group.report.receivedAt,
                    firstWave: point.firstWave.map { String($0.prefix(96)) },
                    maximumWave: point.maximumWave.map { String($0.prefix(96)) })
            }
            selected.append(WatchEventSnapshot(id: event.id, category: WatchEventCategory(rawValue: event.category)!,
                title: String((event.category == "earthquake" ? event.numericHypocenter.epicenter ?? event.displayTitle : event.displayTitle).prefix(80)),
                summary: String((latestReport?.body ?? "").prefix(240)), occurredAt: event.originTime,
                updatedAt: event.latestReportAt, publicationStatus: String(stateText.prefix(120)),
                isOngoing: continuation.isOngoing, ongoingReason: String(continuation.explanation.prefix(160)),
                ongoingUntil: continuation.until, tsunamiWarning: activeReport.flatMap(warningSummary),
                tsunamiObservations: observations.isEmpty ? nil : observations))
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

    private static func warningSummary(_ report: ReportEntity) -> WatchTsunamiWarning? {
        guard let highest = TsunamiPublication(report: report).warnings.first else { return nil }
        let names = highest.areas.prefix(2).map { String($0.name.prefix(30)) }.joined(separator: "、")
        let remaining = highest.areas.count - 2
        let scope = remaining > 0 ? "\(names) ほか\(remaining)予報区" : names
        return WatchTsunamiWarning(title: highest.level.title, scope: scope,
            issuedAt: report.pressTime ?? report.occurredAt ?? report.receivedAt)
    }

    private static func observationPoint(for group: TsunamiObservation.Group, regions: [String]) -> TsunamiObservation? {
        let points = group.observations.filter { $0.firstWave != nil || $0.maximumWave != nil }
        guard group.kind != .offshore, !regions.isEmpty else { return points.first }
        let local = points.first { point in
            let area = group.kind == .estimate ? point.name : point.section.rows?.first { $0.label == "地域" }?.value
            guard let area else { return false }
            return regions.contains { !$0.isEmpty && area.hasPrefix($0) }
        }
        return local ?? points.first
    }
}
