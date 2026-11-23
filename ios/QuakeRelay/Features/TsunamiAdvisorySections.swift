import SwiftUI

// MARK: - Tsunami

/// Tsunami detail: warning status from the latest VTSE41, forecast zones by
/// category, arrival/high-tide information, coastal and offshore observations,
/// coastal estimates, the related earthquake and the bulletin texts.
struct TsunamiDetailSections: View {
    let event: EventEntity
    let reports: [ReportEntity]
    let continuation: EventContinuation
    let highlightedReportID: String?

    var body: some View {
        let latest = TsunamiPublication.latest(in: reports, eventID: event.id)
        let publication = latest.map { TsunamiPublication(report: $0) }
        let active = publication.map { continuation.reportIDs.contains($0.report.id) && !$0.warnings.isEmpty } ?? false
        TsunamiStatusSection(publication: publication, active: active, continuation: continuation)
        if let latest, !latest.isCancelled {
            ForEach(TsunamiForecastGroup.groups(latest)) { group in
                TsunamiForecastGroupSection(group: group)
            }
        }
        if let information = latestInformation {
            TsunamiArrivalSection(report: information)
        }
        ForEach(observationGroups, id: \.kind.title) { group in
            TsunamiObservationSection(group: group, highlighted: group.report.id == highlightedReportID)
        }
        RelatedEarthquakesSection(reports: latestReports)
        if let report = latestReports.last(where: { !BulletinLayout(bulletin: $0.bulletin).comments.isEmpty }) {
            BulletinCommentsSection(report: report)
        }
        ForEach(latestReports.filter { BulletinRemainderSection.hasContent($0) }) { report in
            BulletinRemainderSection(report: report)
        }
    }

    /// Latest non-cancelled report of each telegram type, oldest first.
    private var latestReports: [ReportEntity] {
        var latest: [String: ReportEntity] = [:]
        for report in reports {
            let key = report.telegramType ?? report.id
            if let current = latest[key], !ReportPublicationOrder.areInAscendingOrder(current, report) { continue }
            latest[key] = report
        }
        return latest.values.filter { !$0.isCancelled }.sorted(by: ReportPublicationOrder.areInAscendingOrder)
    }

    /// The latest VTSE51 carrying forecast zones (arrival estimates and high tide).
    private var latestInformation: ReportEntity? {
        let candidates = reports.filter { $0.telegramType == "VTSE51" }
        guard let latest = candidates.max(by: ReportPublicationOrder.areInAscendingOrder), !latest.isCancelled,
              !BulletinLayout(bulletin: latest.bulletin).tsunamiForecastSections.isEmpty else { return nil }
        return latest
    }

    private var observationGroups: [TsunamiObservation.Group] {
        let groups = TsunamiObservation.groups(in: reports)
        guard let highlightedReportID else { return groups }
        return groups.filter { $0.report.id == highlightedReportID } + groups.filter { $0.report.id != highlightedReportID }
    }
}

private struct TsunamiStatusSection: View {
    let publication: TsunamiPublication?
    let active: Bool
    let continuation: EventContinuation

    var body: some View {
        Section {
            if let publication, active {
                ActiveTsunamiWarnings(publication: publication)
                    .accessibilityElement(children: .contain)
                    .accessibilityIdentifier("detailOngoing")
            } else if let publication, let status = publication.status {
                Label(status, systemImage: publication.report.isCancelled ? "xmark.octagon" : "water.waves")
                    .font(.headline)
                    .foregroundStyle(publication.report.isCancelled ? .orange : .blue)
            }
            if continuation.isOngoing && !active {
                Label("継続中", systemImage: "dot.radiowaves.left.and.right")
                    .font(.headline).foregroundStyle(.orange)
                    .accessibilityIdentifier("detailOngoing")
            } else if continuation.state == .unknown {
                Label("継続状況未確認", systemImage: "exclamationmark.circle").foregroundStyle(.orange)
            }
            Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
            if let report = publication?.report {
                KeyValueRow(label: "警報・注意報等の発表", value: DisplayTime.full(report.occurredAt ?? report.receivedAt), monospaced: true)
                if let until = report.validUntil {
                    KeyValueRow(label: "津波予報の有効期限", value: DisplayTime.full(until), monospaced: true)
                }
                KeyValueRow(label: "発表区分", value: report.publicationLabel)
            }
            if let until = continuation.until, continuation.isOngoing {
                KeyValueRow(label: "表示の継続", value: DisplayTime.full(until) + " まで", monospaced: true)
            }
        } header: {
            Text(active ? "発表中" : continuation.state == .history ? "警報・注意報" : "警報・注意報の状況")
        } footer: {
            Text("受信した全国の最新の津波警報・注意報・予報（VTSE41）に基づきます。取消は解除を意味しません。")
        }
    }
}

/// Highest category first, with the zones of each category.
private struct ActiveTsunamiWarnings: View {
    let publication: TsunamiPublication

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(publication.warnings) { warning in
                let style = TsunamiKindStyle(kind: warning.level.title)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(warning.level.title)
                            .font(warning.level == publication.warnings.first?.level ? .title3.bold() : .headline)
                            .padding(.horizontal, 8).padding(.vertical, 3)
                            .foregroundStyle(style.foreground)
                            .background(style.fill, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                        Text("\(warning.areas.count)予報区").font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
                    }
                    ExpandableNameList(names: warning.areas.map(\.name), limit: 8)
                }
            }
            if publication.areas.contains(where: \.withdrawn) {
                Text("一部の予報区で解除").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
    }
}

private struct TsunamiForecastGroupSection: View {
    let group: TsunamiForecastGroup

    var body: some View {
        Section {
            ForEach(group.entries) { entry in
                TsunamiForecastRow(entry: entry, showsKind: group.style == .released || group.style == .other)
            }
        } header: {
            HStack(spacing: 8) {
                TsunamiKindTag(style: group.style, text: group.title)
                Text("\(group.entries.count)予報区").monospacedDigit()
            }
        }
    }
}

private struct TsunamiForecastRow: View {
    let entry: TsunamiForecastEntry
    var showsKind = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(entry.name).font(.subheadline.weight(.semibold))
                Spacer(minLength: 6)
                if let height = entry.maxHeight {
                    Text(height)
                        .font(.subheadline.weight(.bold))
                        .monospacedDigit()
                        .multilineTextAlignment(.trailing)
                }
            }
            if showsKind {
                Text("発表：" + entry.kind).font(.caption)
            }
            if let previous = entry.previousKind {
                Text("前回：" + previous).font(.caption).foregroundStyle(.secondary)
            }
            if let first = entry.firstWave {
                KeyValueRow(label: "第1波の到達予想", value: first, monospaced: true)
            }
            if entry.maxHeight != nil {
                Text("予想される高さ").font(.caption2).foregroundStyle(.secondary)
            }
            if !entry.stations.isEmpty {
                DisclosureGroup("潮位観測点の到達予想・満潮（\(entry.stations.count)地点）") {
                    ForEach(entry.stations) { station in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(station.name).font(.subheadline)
                            if let arrival = station.arrival { KeyValueRow(label: "到達予想", value: arrival, monospaced: true) }
                            if let tide = station.highTide { KeyValueRow(label: "満潮", value: tide, monospaced: true) }
                        }
                    }
                }
                .font(.subheadline)
            }
            ForEach(Array(entry.otherRows.enumerated()), id: \.offset) { _, row in
                KeyValueRow(label: row.label, value: row.value)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct TsunamiArrivalSection: View {
    let report: ReportEntity

    var body: some View {
        let entries = (report.bulletin?.sections ?? []).compactMap(TsunamiForecastEntry.init(section:))
        Section {
            ForEach(entries) { entry in
                HStack(alignment: .top, spacing: 8) {
                    TsunamiKindTag(style: entry.style, text: TsunamiKindStyle(kind: entry.kind).title)
                    TsunamiForecastRow(entry: entry, showsKind: true)
                }
            }
        } header: {
            Text("到達予想・満潮（津波情報）")
        } footer: {
            Text(TelegramCatalog.label(for: report) + " · " + DisplayTime.full(report.occurredAt ?? report.receivedAt) + " 発表")
        }
    }
}

private struct TsunamiObservationSection: View {
    let group: TsunamiObservation.Group
    let highlighted: Bool
    @State private var showsAll = false

    var body: some View {
        let observations = group.observations
        Section {
            ForEach(Array((showsAll ? observations : Array(observations.prefix(6))).enumerated()), id: \.offset) { _, observation in
                TsunamiObservationRow(observation: observation, estimate: group.kind == .estimate)
            }
            if observations.count > 6 {
                Button(showsAll ? "上位6件に戻す" : "すべての\(group.kind == .estimate ? "予報区" : "地点")を表示（\(observations.count)件）") {
                    showsAll.toggle()
                }
                .font(.subheadline)
                .buttonStyle(.borderless)
            }
        } header: {
            Text(group.kind.title + "（\(observations.count)\(group.kind == .estimate ? "予報区" : "地点")）")
        } footer: {
            Text("上昇中・更新・高い値の地点を先に表示します。" + TelegramCatalog.label(for: group.report) + " · "
                 + DisplayTime.full(group.report.occurredAt ?? group.report.receivedAt) + " 発表")
        }
        .listRowBackground(highlighted ? Color.accentColor.opacity(0.10) : nil)
    }
}

private struct TsunamiObservationRow: View {
    let observation: TsunamiObservation
    let estimate: Bool

    var body: some View {
        let rows = observation.section.rows ?? []
        let region = rows.first { $0.label == "地域" }?.value
        let sensor = rows.first { $0.label == "観測方法" }?.value
        let known: Set<String> = ["地域", "観測方法", "第1波", "最大波", "第1波の推定", "最大波の推定"]
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(observation.name).font(.subheadline.weight(.semibold))
                Spacer()
                if let region { Text(region).font(.caption).foregroundStyle(.secondary) }
            }
            if let maximum = observation.maximumWave {
                KeyValueRow(label: estimate ? "最大波の推定" : "最大波", value: maximum, monospaced: true,
                            emphasized: true, valueColor: maximum.contains("上昇中") ? Color.red : nil)
            }
            if let first = observation.firstWave {
                KeyValueRow(label: estimate ? "第1波の推定" : "第1波", value: first, monospaced: true)
            }
            if let sensor { Text("観測方法：" + sensor).font(.caption).foregroundStyle(.secondary) }
            ForEach(Array(rows.filter { !known.contains($0.label) }.enumerated()), id: \.offset) { _, row in
                KeyValueRow(label: row.label, value: row.value)
            }
        }
        .padding(.vertical, 2)
    }
}

/// 関連する地震 sections of tsunami and advisory bulletins, deduplicated.
struct RelatedEarthquakesSection: View {
    let reports: [ReportEntity]

    var body: some View {
        let sections = unique
        if !sections.isEmpty {
            Section("関連する地震") {
                ForEach(Array(sections.enumerated()), id: \.offset) { _, section in
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                            KeyValueRow(label: row.label, value: row.value, monospaced: true)
                        }
                    }
                    .padding(.vertical, 2)
                }
            }
        }
    }

    private var unique: [BulletinSectionDTO] {
        var result: [BulletinSectionDTO] = []
        for report in reports.reversed() {
            for section in BulletinLayout(bulletin: report.bulletin).relatedEarthquakes where !result.contains(section) {
                result.append(section)
            }
        }
        return result
    }
}

// MARK: - Advisory (南海トラフ・後発地震・地震活動・お知らせ・資料)

struct AdvisoryDetailSections: View {
    let event: EventEntity
    let report: ReportEntity?

    var body: some View {
        if let report {
            let layout = BulletinLayout(bulletin: report.bulletin)
            AdvisoryHeaderSection(event: event, report: report, layout: layout)
            if let content = layout.text(.content) {
                Section("発表内容") { SelectableText(text: content) }
            }
            if let appendix = layout.text(.appendix) {
                Section("補足") { SelectableText(text: appendix) }
            }
            if let mainText = layout.text(.body) {
                Section("本文") { SelectableText(text: mainText) }
            }
            if !layout.earthquakeCounts.isEmpty {
                EarthquakeCountSection(sections: layout.earthquakeCounts)
            }
            RelatedEarthquakesSection(reports: [report])
            BulletinCommentsSection(report: report)
            AdvisoryDocumentSection(report: report, layout: layout)
            if !layout.otherSections.isEmpty {
                Section("その他の記載") {
                    ForEach(Array(layout.otherSections.enumerated()), id: \.offset) { _, section in
                        GenericSectionView(section: section)
                    }
                }
            }
        } else {
            Section {
                Text("発表内容はまだ同期されていません。").foregroundStyle(.secondary)
            }
        }
    }
}

private struct SelectableText: View {
    let text: String

    var body: some View {
        Text(text)
            .font(.body)
            .fixedSize(horizontal: false, vertical: true)
            .textSelection(.enabled)
            .padding(.vertical, 2)
    }
}

private struct AdvisoryHeaderSection: View {
    let event: EventEntity
    let report: ReportEntity
    let layout: BulletinLayout

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 6) {
                Text(event.displayTitle).font(.title3.bold()).fixedSize(horizontal: false, vertical: true)
                if let kind = layout.text(.infoKind) {
                    Text(kind)
                        .font(.headline)
                        .padding(.horizontal, 8).padding(.vertical, 4)
                        .foregroundStyle(AdvisoryStyle.foreground(kind))
                        .background(AdvisoryStyle.color(kind), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                }
                HStack(spacing: 6) {
                    StatusBadge(text: report.publicationLabel, color: report.isCancelled ? .orange : .blue)
                    if let short = TelegramCatalog.short(report.telegramType) {
                        StatusBadge(text: short, color: .secondary)
                    }
                }
            }
            .padding(.vertical, 4)
            KeyValueRow(label: "発表時刻", value: DisplayTime.full(report.occurredAt ?? report.receivedAt), monospaced: true)
            if let next = layout.text(.nextAdvisory) {
                NoticeBox(text: next, title: "次の発表", systemImage: "clock", color: .blue)
            }
            if let headline = layout.headline {
                TitledText(title: "見出し", text: headline)
            }
            if report.isCancelled {
                Label("この情報は取り消されました", systemImage: "xmark.octagon.fill").foregroundStyle(.orange)
            }
        }
    }
}

enum AdvisoryStyle {
    static func color(_ kind: String) -> Color {
        if kind.contains("巨大地震警戒") { return .red }
        if kind.contains("巨大地震注意") || kind.contains("後発地震注意") { return .orange }
        if kind.contains("調査中") { return .yellow }
        if kind.contains("調査終了") { return .gray }
        return .blue
    }

    static func foreground(_ kind: String) -> Color {
        kind.contains("調査中") ? .black : .white
    }
}

private struct EarthquakeCountSection: View {
    let sections: [BulletinSectionDTO]

    var body: some View {
        Section("地震回数") {
            ForEach(Array(sections.enumerated()), id: \.offset) { _, section in
                VStack(alignment: .leading, spacing: 3) {
                    Text(section.title.removingPrefix("地震回数：") ?? section.title)
                        .font(.subheadline.weight(.semibold))
                    ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                        KeyValueRow(label: row.label, value: row.value, monospaced: true)
                    }
                }
                .padding(.vertical, 2)
            }
        }
    }
}

private struct AdvisoryDocumentSection: View {
    let report: ReportEntity
    let layout: BulletinLayout

    var body: some View {
        let items = layout.textItems.filter { [BulletinTextRole.receipt, .rawText, .numericData].contains($0.role) }
        if !items.isEmpty || layout.document != nil {
            Section("受信資料") {
                ForEach(items) { item in
                    if item.role == .rawText {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(item.role.rawValue).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                            Text(item.text)
                                .font(.caption.monospaced())
                                .textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    } else {
                        TitledText(title: item.role.rawValue, text: item.text)
                    }
                }
                SourceDocumentButton(report: report)
            }
        }
    }
}
