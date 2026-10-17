import Foundation

struct EventContinuation: Equatable {
    enum State: Equatable { case ongoing, ended, unknown, history }
    var state: State
    var explanation: String
    var until: Date? = nil
    var reportIDs: Set<String> = []
    var isOngoing: Bool { state == .ongoing }

    static let history = EventContinuation(state: .history, explanation: "受信した発表の履歴です。")
}

// The event aggregate can point at an observation or another telegram type.
// Resolve publication lifecycles from their own streams instead.
struct EventContinuationIndex {
    static let eewDisplayDuration: TimeInterval = 60
    let reportsByEvent: [String: [ReportEntity]]
    let latestTsunami: ReportEntity?

    init(reports: [ReportEntity]) {
        reportsByEvent = Dictionary(grouping: reports, by: \.eventId)
        // A VTSE41 publication contains the current national area snapshot.
        // Its earthquake ID set can change on release; never leave the older
        // event active merely because its hash differs from the release's hash.
        latestTsunami = reports.filter { $0.telegramType == "VTSE41" }
            .max(by: ReportPublicationOrder.areInAscendingOrder)
    }

    func status(for event: EventEntity, now: Date) -> EventContinuation {
        switch event.category {
        case "tsunami":
            return tsunami(eventID: event.id, now: now)
        case "earthquake":
            return earthquake(reportsByEvent[event.id] ?? [], now: now)
        default:
            return .history
        }
    }

    private func tsunami(eventID: String, now: Date) -> EventContinuation {
        guard let report = latestTsunami, report.eventId == eventID else { return .history }
        guard !report.isCancelled else {
            return EventContinuation(state: .unknown, explanation: "情報取消。警報・注意報の解除を意味しません。")
        }
        let publication = TsunamiPublication(report: report)
        if publication.areas.contains(where: \.warning) {
            return EventContinuation(state: .ongoing,
                explanation: "津波警報・注意報が継続中です（受信した最新発表に基づきます）。", reportIDs: [report.id])
        }
        let forecasts = publication.areas.filter { $0.kind.contains("津波予報") }
        if !forecasts.isEmpty {
            guard let until = report.validUntil else {
                return EventContinuation(state: .unknown, explanation: "津波予報の終了時刻を確認できません。最新の発表を確認してください。")
            }
            guard now < until else {
                return EventContinuation(state: .ended, explanation: "津波予報に記載された有効期間は終了しました。")
            }
            return EventContinuation(state: .ongoing,
                explanation: publication.areas.contains(where: \.withdrawn)
                    ? "警報・注意報は解除され、津波予報が継続中です。" : "津波予報が継続中です。",
                until: until, reportIDs: [report.id])
        }
        if publication.areas.contains(where: \.withdrawn) {
            return EventContinuation(state: .ended, explanation: "警報・注意報の解除を受信しました。")
        }
        return EventContinuation(state: .unknown, explanation: "継続状況を判定できる予報区情報がありません。")
    }

    private func earthquake(_ reports: [ReportEntity], now: Date) -> EventContinuation {
        let candidates = reports.filter(\.isEEW)
        guard !candidates.isEmpty else { return .history }
        let hasModernForecast = candidates.contains { $0.telegramType == "VXSE45" }
        let streams = Dictionary(grouping: candidates.filter {
            !hasModernForecast || $0.telegramType != "VXSE44"
        }) { report in
            report.telegramType ?? report.classification ?? (report.eventType == "eew_warning" ? "eew.warning" : "eew.forecast")
        }
        let latest = streams.values.compactMap { stream -> ReportEntity? in
            stream.sorted { $0.serverSequence < $1.serverSequence }.reduce(nil as ReportEntity?) { current, report in
                guard let current else { return report }
                return Self.replaces(report, current) ? report : current
            }
        }
        // A non-cancelled final forecast ends announcements for the earthquake.
        // Cancelling a single forecast stream does not cancel its warning stream.
        if latest.contains(where: { !$0.isCancelled && $0.isFinal && $0.eventType != "eew_warning" }) {
            return EventContinuation(state: .ended, explanation: "緊急地震速報の最終報を受信しました。")
        }
        let active = latest.filter { report in
            let time = report.occurredAt ?? report.receivedAt
            let until = min(time.addingTimeInterval(Self.eewDisplayDuration), report.validUntil ?? .distantFuture)
            return !report.isCancelled && !report.isFinal && time <= now.addingTimeInterval(5) && now < until
        }
        // Old caches lacked a cancellation's source stream. They cannot safely
        // establish that an earlier anonymous forecast remains current.
        if let anonymousCancel = candidates.filter({ $0.isCancelled && $0.telegramType == nil && $0.classification == nil })
            .max(by: { $0.serverSequence < $1.serverSequence }),
           active.allSatisfy({ $0.serverSequence < anonymousCancel.serverSequence }) {
            return EventContinuation(state: .unknown, explanation: "旧形式の取消を受信しているため、継続状況は未確認です。")
        }
        guard !active.isEmpty else {
            return EventContinuation(state: .ended, explanation: "緊急地震速報の発表終了、取消、または表示対象期間の経過。")
        }
        let until = active.map {
            min(($0.occurredAt ?? $0.receivedAt).addingTimeInterval(Self.eewDisplayDuration), $0.validUntil ?? .distantFuture)
        }.max()
        return EventContinuation(state: .ongoing, explanation: "緊急地震速報の続報を待っています。",
            until: until, reportIDs: Set(active.map(\.id)))
    }

    private static func replaces(_ report: ReportEntity, _ old: ReportEntity) -> Bool {
        if let revision = report.revision, let previous = old.revision, revision != previous {
            return revision > previous && !(old.isCancelled || old.isFinal && !report.isCancelled)
        }
        if old.isCancelled { return false }
        if report.isCancelled { return true }
        if old.isFinal && !report.isFinal { return false }
        if report.isFinal && !old.isFinal { return true }
        return ReportPublicationOrder.areInAscendingOrder(old, report)
    }
}
