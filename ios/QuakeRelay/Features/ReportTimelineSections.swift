import SwiftUI

// Every received report of an event, newest first. Each row expands to the
// complete content of that single report, so nothing parsed by the VPS is
// out of reach even when the summary above shows only the latest values.

struct ReportTimelineSection: View {
    /// Reports in ascending publication order.
    let reports: [ReportEntity]
    let continuation: EventContinuation
    let highlightedReportID: String?

    var body: some View {
        Section {
            if reports.isEmpty {
                Text("発表履歴はまだ同期されていません。")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(reports.reversed())) { report in
                    ReportTimelineRow(report: report,
                                      highlighted: report.id == highlightedReportID,
                                      ongoing: continuation.reportIDs.contains(report.id))
                        .id(report.id)
                }
            }
        } header: {
            Text("発表履歴（新しい順・\(reports.count)件）")
        } footer: {
            Text("EEWは「第○報」、それ以外は発表区分（発表・訂正・遅延・取消）を表示します。各行を開くとその発表の全項目を確認できます。")
        }
    }
}

struct ReportTimelineRow: View {
    let report: ReportEntity
    let highlighted: Bool
    var ongoing = false
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(report.publicationLabel).font(.subheadline.weight(.bold))
                Text(TelegramCatalog.short(report.telegramType) ?? "")
                    .font(.caption).foregroundStyle(.secondary)
                if report.isCancelled { StatusBadge(text: "取消", color: .orange) }
                if report.isEEW && report.isFinal && !report.isCancelled { StatusBadge(text: "最終", color: .blue) }
                if ongoing { StatusBadge(text: "継続中", color: .orange) }
                Spacer(minLength: 4)
                Text(DisplayTime.time(report.occurredAt ?? report.receivedAt))
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Text(report.title).font(.subheadline)
            if let line = keyLine {
                Text(line).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            DisclosureGroup(isExpanded: $expanded) {
                ReportFullContent(report: report)
                    .padding(.top, 4)
            } label: {
                Text("この発表の全項目").font(.caption.weight(.semibold))
            }
            Text("受信 " + DisplayTime.daySeconds(report.receivedAt) + " · seq \(report.serverSequence)")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
        }
        .leadingBar(markerColor)
        .padding(.vertical, 4)
        .listRowBackground(highlighted ? Color.accentColor.opacity(0.12) : nil)
        .onAppear { if highlighted { expanded = true } }
    }

    private var markerColor: Color {
        if report.isCancelled { return .gray }
        switch report.telegramType ?? "" {
        case "VXSE43": return .red
        case "VXSE44", "VXSE45": return .orange
        case "VTSE41":
            let top = TsunamiPublication(report: report).warnings.first.map { TsunamiKindStyle(kind: $0.level.title) }
            return top?.fill ?? .blue
        case "VTSE51", "VTSE52": return .cyan
        case "VYSE50", "VYSE51", "VYSE52", "VYSE60": return .purple
        default:
            return IntensityReading(report.maxIntensity)?.dominant?.fill ?? .secondary
        }
    }

    private var keyLine: String? {
        var parts: [String] = []
        if let value = report.numericHypocenter {
            if let epicenter = value.epicenter { parts.append(value.isAssumed ? epicenter + "（仮定）" : epicenter) }
            if EEWStream(report) != .warning || !report.isEEW {
                if let magnitude = ValueText.magnitude(value) { parts.append(magnitude) }
                if let depth = value.depthText { parts.append("深さ" + depth) }
            }
        }
        if let intensity = IntensityReading(report.maxIntensity) {
            parts.append((report.isEEW ? "予想最大震度" : "最大震度") + intensity.displayText)
        }
        if let status = TsunamiPublication(report: report).status { parts.append(status) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

/// Complete content of one report: notification text, numeric fields,
/// affected areas, every bulletin section and the telegram metadata.
struct ReportFullContent: View {
    let report: ReportEntity
    @State private var showingObservations = false

    var body: some View {
        let layout = BulletinLayout(bulletin: report.bulletin)
        let tree = IntensityObservationTree(report: report)
        let related = report.relatedEarthquakes ?? []
        VStack(alignment: .leading, spacing: 8) {
            Group {
                TitledText(title: "通知本文", text: report.body)
                if let value = report.numericHypocenter {
                    HypocenterRows(value: value, showMagnitudeAndDepth: EEWStream(report) != .warning || !report.isEEW)
                }
                if let intensity = IntensityReading(report.maxIntensity) {
                    KeyValueRow(label: report.isEEW ? "予想最大震度" : "最大震度", value: intensity.displayText, emphasized: true)
                }
                AffectedAreaRows(report: report)
                if let headline = layout.headline {
                    TitledText(title: "見出し", text: headline)
                }
                ForEach(layout.textItems) { item in
                    TitledText(title: item.role.rawValue, text: item.text)
                }
            }
            Group {
                if !tree.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("各地の観測").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                        ForEach(tree.groups(atDepth: tree.summaryDepth)) { group in
                            IntensityGroupRow(reading: group.reading, names: group.names)
                        }
                        Button("すべての地域・観測点（\(tree.leaves.count)件）") { showingObservations = true }
                            .font(.caption)
                            .buttonStyle(.borderless)
                            .accessibilityIdentifier("timelineObservations-" + report.id)
                    }
                }
                ForEach(Array(layout.longPeriodRows.enumerated()), id: \.offset) { _, row in
                    KeyValueRow(label: row.label, value: row.value)
                }
                ForEach(layout.tsunamiForecastSections.compactMap(TsunamiForecastEntry.init(section:))) { entry in
                    CompactTsunamiForecast(entry: entry)
                }
                ForEach(Array(related.enumerated()), id: \.offset) { _, value in
                    VStack(alignment: .leading, spacing: 3) {
                        Text("関連する地震").font(.subheadline.weight(.semibold))
                        HypocenterRows(value: value)
                    }
                }
                ForEach(Array(genericSections(layout, structuredRelated: !related.isEmpty).enumerated()),
                        id: \.offset) { _, section in
                    GenericSectionView(section: section)
                }
            }
            Divider()
            Group {
                Text("電文情報").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                ForEach(ReportMetadata.rows(for: report)) { row in
                    KeyValueRow(label: row.label, value: row.value, monospaced: row.monospaced)
                }
                SourceDocumentButton(report: report)
            }
        }
        .font(.subheadline)
        .sheet(isPresented: $showingObservations) {
            IntensityObservationsView(tree: tree, title: "各地の観測")
                .presentationDetents([.medium, .large])
        }
    }

    /// Sections shown as-is. Structured related earthquakes replace their bulletin rows.
    private func genericSections(_ layout: BulletinLayout, structuredRelated: Bool) -> [BulletinSectionDTO] {
        var sections: [BulletinSectionDTO] = layout.tsunamiObservationSections
        sections += layout.tsunamiEstimateSections
        if !structuredRelated { sections += layout.relatedEarthquakes }
        sections += layout.earthquakeCounts
        sections += layout.otherSections
        return sections
    }
}

struct HypocenterRows: View {
    let value: HypocenterDTO
    var showMagnitudeAndDepth = true

    var body: some View {
        if let note = value.qualification {
            NoticeBox(text: note, systemImage: "exclamationmark.triangle")
        }
        if let epicenter = value.epicenter { KeyValueRow(label: value.label("震源地名"), value: epicenter) }
        if let origin = ValueText.originTime(value) {
            KeyValueRow(label: value.label("地震発生時刻"), value: DisplayTime.full(origin), monospaced: true)
        }
        if let coordinate = ValueText.coordinate(value) {
            KeyValueRow(label: value.label("震央"), value: coordinate, monospaced: true)
        }
        if showMagnitudeAndDepth {
            if let magnitude = ValueText.magnitude(value) {
                KeyValueRow(label: value.label("マグニチュード"), value: magnitude, monospaced: true)
            }
            if let depth = value.depthText { KeyValueRow(label: value.label("深さ"), value: depth, monospaced: true) }
        }
        HypocenterDetailRows(value: value)
    }
}

/// Source details of a hypocenter, shown only when the VPS provided them.
/// Warnings (VXSE43) never carry magnitude or depth details here.
struct HypocenterDetailRows: View {
    let value: HypocenterDTO

    static func hasContent(_ value: HypocenterDTO) -> Bool {
        let texts = [value.arrivalTime, value.coordinateText, value.detailedName, value.nameFromMark,
                     value.landOrSea, value.source, value.magnitudeType, value.code]
        return texts.contains { $0?.isEmpty == false } || ValueText.accuracy(value) != nil
    }

    var body: some View {
        if let arrival = ServerDateParser.parse(value.arrivalTime) {
            KeyValueRow(label: "発現時刻", value: DisplayTime.full(arrival), monospaced: true)
        }
        if let text = value.coordinateText, !text.isEmpty {
            KeyValueRow(label: value.label("震源の記述"), value: text)
        }
        if let name = value.detailedName, !name.isEmpty { KeyValueRow(label: "詳細な震央地名", value: name) }
        if let mark = value.nameFromMark, !mark.isEmpty { KeyValueRow(label: "震央の補足", value: mark) }
        if let area = value.landOrSea, !area.isEmpty { KeyValueRow(label: "陸域・海域", value: area) }
        if let source = value.source, !source.isEmpty { KeyValueRow(label: "震源の決定機関", value: source) }
        if let type = value.magnitudeType, !type.isEmpty { KeyValueRow(label: "マグニチュードの種別", value: type, monospaced: true) }
        if let accuracy = ValueText.accuracy(value) {
            KeyValueRow(label: "震源の精度（ランク）", value: accuracy, monospaced: true)
        }
        if let code = value.code, !code.isEmpty { KeyValueRow(label: "震央地名コード", value: code, monospaced: true) }
    }
}

/// Region list carried as affected_areas (EEW forecast intensities or warning areas).
private struct AffectedAreaRows: View {
    let report: ReportEntity

    var body: some View {
        if report.isEEW, let detail = report.eew, !report.isCancelled {
            if let headline = detail.headline, !headline.isEmpty { TitledText(title: "見出し", text: headline) }
            if let longPeriod = EEWPresentation.longPeriodText(detail.forecastMaxLongPeriodClass) {
                KeyValueRow(label: "予想最大長周期地震動階級", value: longPeriod, monospaced: true)
            }
            if let change = detail.change?.description, !change.isEmpty {
                KeyValueRow(label: "予想の変化", value: change)
            }
            ForEach(EEWPresentation.warningAreaGroups(detail)) { group in
                KeyValueRow(label: "警報対象（" + group.title + "）", value: group.names.joined(separator: "、"))
            }
            let areas = EEWPresentation.structuredAreas(detail)
            if !areas.isEmpty {
                EEWAreaList(areas: areas, title: "地域別の予想（\(areas.count)地域）", limit: 8)
            }
            ForEach(Array((detail.comments ?? []).enumerated()), id: \.offset) { _, comment in
                TitledText(title: "付加文", text: comment)
            }
        }
        // Reports parsed before the VPS provided structured EEW values.
        if report.isEEW && report.eew == nil {
            let forecast = EEWPresentation.forecastAreas(report)
            let named = EEWPresentation.namedAreas(report)
            if !forecast.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("地域別の予想震度").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ForEach(Array(forecast.enumerated()), id: \.offset) { _, area in
                        HStack(spacing: 8) {
                            IntensityBadge(reading: IntensityReading(area.maxIntensity), size: 24)
                            Text(area.name).font(.subheadline)
                        }
                    }
                }
            }
            if !named.isEmpty {
                KeyValueRow(label: EEWStream(report) == .warning ? "警報の対象地域" : "記載地域",
                            value: named.joined(separator: "、"))
            }
        }
    }
}

private struct CompactTsunamiForecast: View {
    let entry: TsunamiForecastEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                TsunamiKindTag(style: entry.style, text: TsunamiKindStyle(kind: entry.kind).title)
                Text(entry.name).font(.subheadline.weight(.semibold))
            }
            Text(entry.kind + (entry.previousKind.map { "（前回：" + $0 + "）" } ?? ""))
                .font(.caption).foregroundStyle(.secondary)
            if let height = entry.maxHeight { KeyValueRow(label: "予想される高さ", value: height, monospaced: true) }
            if let first = entry.firstWave { KeyValueRow(label: "第1波の到達予想", value: first, monospaced: true) }
            ForEach(entry.stations) { station in
                KeyValueRow(label: station.name,
                            value: [station.arrival.map { "到達予想 " + $0 }, station.highTide.map { "満潮 " + $0 }]
                                .compactMap { $0 }.joined(separator: "\n"),
                            monospaced: true)
            }
            ForEach(Array(entry.otherRows.enumerated()), id: \.offset) { _, row in
                KeyValueRow(label: row.label, value: row.value)
            }
        }
    }
}

/// Telegram metadata of the latest report.
struct ReportMetadataSection: View {
    let report: ReportEntity
    var showsSourceButton = true

    var body: some View {
        Section {
            ForEach(ReportMetadata.rows(for: report)) { row in
                KeyValueRow(label: row.label, value: row.value, monospaced: row.monospaced)
            }
            if showsSourceButton {
                SourceDocumentButton(report: report)
            }
        } header: {
            Text("電文情報（最新の発表）")
        } footer: {
            Text("値は長押しで選択・コピーできます。受信までの時間は配信時刻（なければ発表時刻）から受信までです。")
        }
    }
}
