import Foundation

struct ReportChange: Identifiable, Equatable {
    var id: String { label }
    let label: String
    let previous: String
    let current: String
}

struct TsunamiPublication {
    struct Area: Identifiable {
        var id: String { name }
        let name: String
        let kind: String
        let previousKind: String?
        let height: String?
        var warning: Bool { ["大津波警報", "津波警報", "津波注意報"].contains(kind) }
        var withdrawn: Bool {
            kind.contains("解除") || (kind.contains("津波予報") &&
                previousKind.map { ["大津波警報", "津波警報", "津波注意報"].contains($0) } == true)
        }
    }
    let report: ReportEntity
    var areas: [Area] {
        guard report.telegramType == "VTSE41" || report.telegramType == "VTSE51" else { return [] }
        return (report.bulletin?.sections ?? []).compactMap { section in
            guard section.title.hasPrefix("津波予報区："),
                  let kind = section.rows?.first(where: { $0.label == "発表" })?.value else { return nil }
            return Area(name: String(section.title.dropFirst("津波予報区：".count)), kind: kind,
                previousKind: section.rows?.first(where: { $0.label == "前回" })?.value,
                height: section.rows?.first(where: { $0.label == "予想される高さ" })?.value)
        }
    }
    var status: String? {
        guard report.telegramType == "VTSE41" else { return nil }
        if report.isCancelled { return "情報取消（解除ではありません）" }
        let released = areas.filter(\.withdrawn)
        if !released.isEmpty {
            return areas.contains(where: \.warning) ? "一部地域で解除・他地域に警報や注意報" : "警報・注意報の解除を発表"
        }
        if areas.contains(where: \.warning) { return "津波警報・注意報を発表" }
        if !areas.isEmpty && areas.allSatisfy({ $0.kind.contains("津波予報") }) { return "津波予報（海面変動に注意）" }
        return nil
    }
    static func latest(in reports: [ReportEntity], eventID: String) -> ReportEntity? {
        reports.filter { $0.eventId == eventID && $0.telegramType == "VTSE41" }
            .max(by: ReportPublicationOrder.areInAscendingOrder)
    }
}

enum ReportComparison {
    static func previous(to current: ReportEntity, in reports: [ReportEntity]) -> ReportEntity? {
        // Compare a telegram series with itself. Observation and forecast products
        // and VXSE43/44/45 serial numbers are independent.
        guard let type = current.telegramType else { return nil }
        return reports.filter {
            $0.eventId == current.eventId && $0.telegramType == type && $0.id != current.id &&
            $0.serverSequence < current.serverSequence &&
            ReportTimelineOrder.areInAscendingOrder($0, current) &&
            (!current.isEEW || ($0.revision ?? 0) <= (current.revision ?? Int.max))
        }.max(by: ReportPublicationOrder.areInAscendingOrder)
    }
    static func changes(from previous: ReportEntity, to current: ReportEntity) -> [ReportChange] {
        guard previous.eventId == current.eventId && previous.telegramType == current.telegramType else { return [] }
        var result: [ReportChange] = []
        func append(_ label: String, _ old: String?, _ new: String?) {
            guard old != new, old != nil || new != nil else { return }
            result.append(ReportChange(label: label, previous: old ?? "記載なし", current: new ?? "記載なし"))
        }
        append("発表区分", previous.publicationLabel, current.publicationLabel)
        append("取消", previous.isCancelled ? "取消" : "有効な発表", current.isCancelled ? "取消（解除ではありません）" : "有効な発表")
        if current.isEEW { append("最終報", previous.isFinal ? "最終" : "続報", current.isFinal ? "最終" : "続報") }
        // A cancellation does not turn omitted numerical fields into new estimates.
        guard !current.isCancelled else { return result }
        let old = previous.numericHypocenter, new = current.numericHypocenter
        append("震源地", old?.epicenter, new?.epicenter)
        append("震源の精度", old?.qualification, new?.qualification)
        append("緯度", old?.latitude.map { String($0) }, new?.latitude.map { String($0) })
        append("経度", old?.longitude.map { String($0) }, new?.longitude.map { String($0) })
        append("深さ", old?.depthText, new?.depthText)
        append("マグニチュード", old?.magnitude.map { String($0) }, new?.magnitude.map { String($0) })
        append(current.isEEW ? "予想最大震度" : "観測最大震度", previous.maxIntensity, current.maxIntensity)
        let oldRows = rows(previous.bulletin), newRows = rows(current.bulletin)
        for key in Set(oldRows.keys).union(newRows.keys).sorted() { append(key, oldRows[key], newRows[key]) }
        return result
    }
    private static func rows(_ bulletin: BulletinDTO?) -> [String: String] {
        var values: [String: String] = [:]
        if let headline = bulletin?.headline, !headline.isEmpty { values["発表の要約"] = headline }
        for section in bulletin?.sections ?? [] {
            // The large observation list stays in its sheet. Regional max values
            // are useful as change summaries; individual station rows are omitted.
            if section.title.hasPrefix("観測") && section.rows?.contains(where: { $0.label == "震度" }) == true { continue }
            if let text = section.text, !text.isEmpty { values[section.title] = text }
            for row in section.rows ?? [] {
                if row.label == "前回" { continue }
                let key = section.title + " / " + row.label
                if let existing = values[key] { values[key] = existing + " / " + row.value }
                else { values[key] = row.value }
            }
        }
        return values
    }
}

// The display timeline keeps every received report. State comparisons use source
// serials and correction/cancellation precedence so a late old report cannot win.
enum ReportPublicationOrder {
    static func areInAscendingOrder(_ lhs: ReportEntity, _ rhs: ReportEntity) -> Bool {
        if lhs.isEEW, rhs.isEEW, let a = lhs.revision, let b = rhs.revision, a != b { return a < b }
        let a = lhs.occurredAt ?? lhs.receivedAt, b = rhs.occurredAt ?? rhs.receivedAt
        if a != b { return a < b }
        if !lhs.isEEW, let a = lhs.pressTime, let b = rhs.pressTime, a != b { return a < b }
        func rank(_ report: ReportEntity) -> Int {
            report.isCancelled ? 3 : report.infoType == "訂正" ? 2 : report.isFinal ? 1 : 0
        }
        if rank(lhs) != rank(rhs) { return rank(lhs) < rank(rhs) }
        return lhs.serverSequence < rhs.serverSequence
    }
}
