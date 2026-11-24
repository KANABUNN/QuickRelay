import SwiftUI

// Earthquake detail: one screen consolidating EEW, 震度速報, 震源・震度情報,
// 震源要素更新 and 長周期地震動 for the same earthquake. Each block names the
// telegram it comes from, so values of different products are never mixed.

struct EarthquakeDetailSections: View {
    let event: EventEntity
    let reports: [ReportEntity]
    let continuation: EventContinuation

    var body: some View {
        let summary = EarthquakeEventSummary(reports: reports)
        EarthquakeHeaderSection(event: event, summary: summary, continuation: continuation)
        if summary.hasEEW {
            EEWSection(eventID: event.id, summary: summary, reports: reports, continuation: continuation)
        }
        if let report = summary.commentReport {
            BulletinCommentsSection(report: report)
        }
        if let report = summary.observedReport {
            ObservedIntensitySection(report: report)
        }
        if let report = summary.longPeriodReport {
            LongPeriodSection(report: report)
        }
        ForEach(remainingReports(summary)) { report in
            BulletinRemainderSection(report: report)
        }
        if !summary.cancelledTypes.isEmpty {
            Section("取消") {
                ForEach(summary.cancelledTypes, id: \.self) { type in
                    Label("\(TelegramCatalog.name(type) ?? type)（\(type)）の最新の発表は取消です。",
                          systemImage: "xmark.octagon")
                        .font(.subheadline)
                        .foregroundStyle(.orange)
                }
                Text("取り消された発表の数値は上の表示に使っていません。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    /// Latest non-EEW reports whose texts or unrecognised sections are not shown above.
    private func remainingReports(_ summary: EarthquakeEventSummary) -> [ReportEntity] {
        summary.latestByType.values
            .filter { !$0.isCancelled && BulletinRemainderSection.hasContent($0) }
            .sorted(by: ReportPublicationOrder.areInAscendingOrder)
    }
}

// MARK: - Header

private struct EarthquakeHeaderSection: View {
    let event: EventEntity
    let summary: EarthquakeEventSummary
    let continuation: EventContinuation

    var body: some View {
        let hypocenter = summary.hypocenterReport?.numericHypocenter
        let observed = summary.observedMax
        let forecast = summary.forecastMax
        let warningOnly = summary.hypocenterReport.map { EEWStream($0) == .warning && $0.isEEW } ?? false
        Section {
            HStack(alignment: .top, spacing: 14) {
                IntensityBadge(reading: observed ?? forecast, size: 68,
                               caption: observed != nil ? "最大震度" : forecast != nil ? "予想最大" : nil,
                               longPeriodText: summary.maxLongPeriodText)
                VStack(alignment: .leading, spacing: 4) {
                    Text(hypocenter?.epicenter ?? event.displayTitle)
                        .font(.title3.bold())
                        .fixedSize(horizontal: false, vertical: true)
                    if hypocenter?.isAssumed == true {
                        StatusBadge(text: "仮定震源", color: .orange)
                    }
                    if let origin = ValueText.originTime(hypocenter) {
                        Text("発生 " + DisplayTime.full(origin))
                            .font(.subheadline.monospacedDigit())
                    }
                    if summary.hypocenterIsFromEEW {
                        Text("緊急地震速報による値")
                            .font(.caption).foregroundStyle(.orange)
                    }
                }
            }
            .padding(.vertical, 4)

            HStack(spacing: 8) {
                MetricTile(title: hypocenter?.label("マグニチュード") ?? "マグニチュード",
                           value: warningOnly ? "非表示" : (ValueText.magnitude(hypocenter) ?? "−"))
                MetricTile(title: hypocenter?.label("深さ") ?? "深さ",
                           value: warningOnly ? "非表示" : (hypocenter?.depthText ?? "−"))
                if let observed, let forecast {
                    MetricTile(title: "EEW予想最大", value: forecast.displayText)
                        .accessibilityHint("観測最大震度は\(observed.displayText)")
                }
            }

            Group {
                if continuation.isOngoing {
                    Label("継続中", systemImage: "dot.radiowaves.left.and.right")
                        .font(.headline).foregroundStyle(.orange)
                        .accessibilityIdentifier("detailOngoing")
                    Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
                } else if continuation.state == .unknown {
                    Label("継続状況未確認", systemImage: "exclamationmark.circle").foregroundStyle(.orange)
                    Text(continuation.explanation).font(.caption)
                }
                if let note = hypocenter?.qualification {
                    NoticeBox(text: note, systemImage: "exclamationmark.triangle")
                }
                if warningOnly {
                    NoticeBox(text: "緊急地震速報（警報）のマグニチュード・深さは表示しません。予報の値は「緊急地震速報」の欄で確認できます。",
                              systemImage: "info.circle", color: .secondary)
                }
            }
            Group {
                if let coordinate = ValueText.coordinate(hypocenter) {
                    KeyValueRow(label: hypocenter?.label("震央") ?? "震央", value: coordinate, monospaced: true)
                }
                if let report = summary.hypocenterReport {
                    KeyValueRow(label: "震源の出典",
                                value: TelegramCatalog.label(for: report) + "\n" + DisplayTime.full(report.occurredAt ?? report.receivedAt) + " 発表")
                }
                if let observed, let report = summary.observedReport {
                    KeyValueRow(label: "観測最大震度", value: observed.displayText + "（" + (TelegramCatalog.name(report.telegramType) ?? report.title) + "）")
                }
                KeyValueRow(label: event.isEEW ? "最新報" : "発表区分", value: event.publicationLabel)
                KeyValueRow(label: "最新の発表", value: DisplayTime.full(event.latestReportAt), monospaced: true)
            }
            if event.isCancelled {
                Label("最新の電文は取り消されました", systemImage: "xmark.octagon.fill").foregroundStyle(.orange)
            } else if event.isEEW && event.isFinal {
                Label("最終報", systemImage: "checkmark.circle.fill").foregroundStyle(.blue)
            }
            let products = Array(Set(reports.compactMap(\.telegramType))).sorted()
            if !products.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("受信した情報").font(.caption).foregroundStyle(.secondary)
                    ProductTags(codes: products)
                }
            }
        }
    }

    private var reports: [ReportEntity] {
        Array(summary.latestByType.values) + [summary.forecastReport, summary.warningReport].compactMap { $0 }
    }
}

// MARK: - EEW

private struct EEWSection: View {
    let eventID: String
    let summary: EarthquakeEventSummary
    let reports: [ReportEntity]
    let continuation: EventContinuation
    @State private var showingAll = false

    var body: some View {
        let entries = EEWPresentation.reports(reports)
        Section {
            if let warning = summary.warningReport {
                EEWWarningBlock(report: warning, ongoing: continuation.reportIDs.contains(warning.id))
            }
            if let forecast = summary.forecastReport {
                EEWForecastBlock(report: forecast, ongoing: continuation.reportIDs.contains(forecast.id),
                                 reference: summary.warningReport != nil && !(summary.warningReport?.isCancelled ?? true))
            }
            if let forecast = summary.forecastReport, !forecast.isCancelled {
                let areas = EEWPresentation.forecastAreas(forecast)
                if !areas.isEmpty {
                    EEWForecastAreas(areas: areas)
                } else if forecast.affectedAreas == nil {
                    Text("地域別の予想震度は、この版のアプリで受信した報から表示します。")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            if !entries.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("報の推移（新しい順）").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    EEWHistoryGrid(entries: Array(entries.reversed().prefix(6)))
                    if entries.count > 6 {
                        Button("すべての報を見る（\(entries.count)件）") { showingAll = true }
                            .font(.subheadline)
                            .buttonStyle(.borderless)
                            .sheet(isPresented: $showingAll) {
                                NavigationStack {
                                    ScrollView {
                                        EEWHistoryGrid(entries: Array(entries.reversed())).padding()
                                    }
                                    .navigationTitle("緊急地震速報の推移")
                                    .navigationBarTitleDisplayMode(.inline)
                                    .toolbar {
                                        ToolbarItem(placement: .confirmationAction) { Button("閉じる") { showingAll = false } }
                                    }
                                }
                                .presentationDetents([.large])
                            }
                    }
                }
            }
        } header: {
            Text("緊急地震速報")
        } footer: {
            Text("予報・警報の報数は別々に数えられます。警報のマグニチュード・深さは表示しません。震動の到達までの秒読みは表示しません。")
        }
    }
}

private struct EEWWarningBlock: View {
    let report: ReportEntity
    let ongoing: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Label("警報", systemImage: "exclamationmark.triangle.fill")
                    .font(.headline)
                    .foregroundStyle(report.isCancelled ? Color.secondary : Color.red)
                if let revision = report.revision { Text("第\(revision)報").font(.subheadline.monospacedDigit()) }
                if report.isCancelled { StatusBadge(text: "取消", color: .orange) }
                if ongoing { StatusBadge(text: "継続中", color: .orange) }
                Spacer()
                Text(DisplayTime.time(report.occurredAt ?? report.receivedAt))
                    .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            if !report.isCancelled {
                if let value = report.numericHypocenter {
                    if let epicenter = value.epicenter { KeyValueRow(label: value.label("震源"), value: epicenter) }
                    if let origin = ValueText.originTime(value) {
                        KeyValueRow(label: value.label("発生時刻"), value: DisplayTime.full(origin), monospaced: true)
                    }
                    if let note = value.qualification { NoticeBox(text: note, systemImage: "exclamationmark.triangle") }
                }
                if let intensity = IntensityReading(report.maxIntensity) {
                    HStack(spacing: 10) {
                        IntensityBadge(reading: intensity, size: 34)
                        Text("予想最大震度 " + intensity.displayText).font(.subheadline.weight(.semibold))
                    }
                }
                let areas = EEWPresentation.namedAreas(report)
                if !areas.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("警報の対象地域（府県予報区・細分区域）").font(.caption).foregroundStyle(.secondary)
                        FlowLayout(spacing: 5) {
                            ForEach(Array(areas.enumerated()), id: \.offset) { _, name in
                                Text(name)
                                    .font(.caption.weight(.semibold))
                                    .padding(.horizontal, 6).padding(.vertical, 2)
                                    .foregroundStyle(.red)
                                    .background(Color.red.opacity(0.1), in: Capsule())
                            }
                        }
                    }
                }
            } else {
                Text(report.body).font(.subheadline).textSelection(.enabled)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct EEWForecastBlock: View {
    let report: ReportEntity
    let ongoing: Bool
    /// Shown as reference values next to a warning (DMDATA display restriction).
    let reference: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Label(reference ? "予報（参考）" : "予報", systemImage: "waveform.path.ecg")
                    .font(.headline)
                    .foregroundStyle(report.isCancelled ? Color.secondary : Color.orange)
                if let revision = report.revision { Text("第\(revision)報").font(.subheadline.monospacedDigit()) }
                if report.isFinal && !report.isCancelled { StatusBadge(text: "最終", color: .blue) }
                if report.isCancelled { StatusBadge(text: "取消", color: .orange) }
                if ongoing { StatusBadge(text: "継続中", color: .orange) }
                Spacer()
                Text(DisplayTime.time(report.occurredAt ?? report.receivedAt))
                    .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            if report.isCancelled {
                Text(report.body).font(.subheadline).textSelection(.enabled)
            } else {
                Text(report.telegramType == "VXSE44" ? "出所：緊急地震速報（予報 / VXSE44）" : "出所：緊急地震速報（地震動予報 / VXSE45）")
                    .font(.caption).foregroundStyle(.secondary)
                if let value = report.numericHypocenter {
                    if let note = value.qualification { NoticeBox(text: note, systemImage: "exclamationmark.triangle") }
                    if let epicenter = value.epicenter { KeyValueRow(label: value.label("震源"), value: epicenter) }
                    if let origin = ValueText.originTime(value) {
                        KeyValueRow(label: value.label("発生時刻"), value: DisplayTime.full(origin), monospaced: true)
                    }
                    if let magnitude = ValueText.magnitude(value) {
                        KeyValueRow(label: value.label("マグニチュード"), value: magnitude, monospaced: true)
                    }
                    if let depth = value.depthText { KeyValueRow(label: value.label("深さ"), value: depth, monospaced: true) }
                    if let coordinate = ValueText.coordinate(value) {
                        KeyValueRow(label: value.label("震央"), value: coordinate, monospaced: true)
                    }
                }
                if let intensity = IntensityReading(report.maxIntensity) {
                    HStack(spacing: 10) {
                        IntensityBadge(reading: intensity, size: 34)
                        Text("予想最大震度 " + intensity.displayText).font(.subheadline.weight(.semibold))
                    }
                }
                if reference {
                    Text("同じ地震の予報から取得した値です。警報とは発表時刻が異なる場合があります。")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

private struct EEWForecastAreas: View {
    let areas: [AffectedAreaDTO]

    var body: some View {
        let groups = grouped
        VStack(alignment: .leading, spacing: 8) {
            Text("地域別の予想震度（\(areas.count)地域）").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            ForEach(groups) { group in
                IntensityGroupRow(reading: IntensityReading(group.key), names: group.names, badgeCaption: "予想")
            }
            Text("電文に記載された地域だけを表示します。震度4未満の地域は省略される場合があります。")
                .font(.caption2).foregroundStyle(.secondary)
        }
    }

    private struct AreaGroup: Identifiable {
        let key: String
        let names: [String]
        var id: String { key }
    }

    private var grouped: [AreaGroup] {
        var order: [String] = []
        var names: [String: [String]] = [:]
        for area in areas {
            let key = IntensityReading(area.maxIntensity)?.displayText ?? (area.maxIntensity ?? "")
            if names[key] == nil { order.append(key) }
            names[key, default: []].append(area.name)
        }
        return order.map { AreaGroup(key: $0, names: names[$0] ?? []) }
    }
}

/// Report-by-report EEW values. Warning rows never show magnitude or depth.
private struct EEWHistoryGrid: View {
    let entries: [ReportEntity]

    var body: some View {
        Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 4) {
            GridRow {
                Text("報").gridColumnAlignment(.leading)
                Text("発表")
                Text("予想")
                Text("M").gridColumnAlignment(.trailing)
                Text("深さ").gridColumnAlignment(.trailing)
            }
            .font(.caption2.weight(.semibold))
            .foregroundStyle(.secondary)
            ForEach(entries) { report in
                let warning = EEWStream(report) == .warning
                let value = report.numericHypocenter
                Divider().gridCellUnsizedAxes(.horizontal)
                GridRow {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(report.revision.map { "第\($0)報" } ?? "−").font(.caption.monospacedDigit().weight(.semibold))
                        Text(warning ? "警報" : report.telegramType == "VXSE44" ? "予報(旧)" : "予報")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(warning ? Color.red : Color.orange)
                    }
                    Text(DisplayTime.time(report.occurredAt ?? report.receivedAt)).font(.caption.monospacedDigit())
                    IntensityBadge(reading: IntensityReading(report.maxIntensity), size: 28)
                    Text(warning ? "−" : (ValueText.magnitude(value) ?? "−")).font(.caption.monospacedDigit())
                    Text(warning ? "−" : (value?.depthText ?? "−")).font(.caption.monospacedDigit())
                }
                GridRow {
                    Text(rowNote(report))
                        .font(.caption2)
                        .foregroundStyle(report.isCancelled ? Color.orange : Color.secondary)
                        .gridCellColumns(5)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    private func rowNote(_ report: ReportEntity) -> String {
        if report.isCancelled { return "取消" }
        var parts: [String] = []
        if let value = report.numericHypocenter {
            if let epicenter = value.epicenter { parts.append(value.isAssumed ? epicenter + "（仮定）" : epicenter) }
            if value.status == "low_accuracy" { parts.append("低精度") }
        }
        if report.isFinal { parts.append("最終報") }
        return parts.isEmpty ? " " : parts.joined(separator: " · ")
    }
}

// MARK: - Observed intensity

struct ObservedIntensitySection: View {
    let report: ReportEntity
    @State private var showingAll = false

    var body: some View {
        let tree = IntensityObservationTree(sections: BulletinLayout(bulletin: report.bulletin).intensitySections)
        let groups = tree.groups(atDepth: tree.summaryDepth)
        Section {
            if let intensity = IntensityReading(report.maxIntensity) {
                HStack(spacing: 10) {
                    IntensityBadge(reading: intensity, size: 34)
                    Text("観測最大震度 " + intensity.displayText).font(.subheadline.weight(.semibold))
                }
            }
            if groups.isEmpty {
                Text("この発表に地域別の震度の記載はありません。").font(.subheadline).foregroundStyle(.secondary)
            }
            ForEach(groups) { group in
                IntensityGroupRow(reading: group.reading, names: group.names)
            }
            if !tree.isEmpty {
                Button {
                    showingAll = true
                } label: {
                    Label("市町村・観測点まで見る（\(tree.leaves.count)件）", systemImage: "list.bullet.indent")
                        .font(.subheadline)
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("intensityObservations")
                // Attach the sheet to one row: a modifier on a Section applies to every row.
                .sheet(isPresented: $showingAll) {
                    IntensityObservationsView(tree: tree, title: "各地の震度")
                        .presentationDetents([.medium, .large])
                        .presentationDragIndicator(.visible)
                }
            }
        } header: {
            Text(tree.summaryDepth == 2 ? "各地の震度（細分区域）" : "各地の震度")
        } footer: {
            Text((TelegramCatalog.label(for: report)) + " · " + DisplayTime.full(report.occurredAt ?? report.receivedAt) + " 発表")
        }
    }
}

private struct LongPeriodSection: View {
    let report: ReportEntity
    @State private var showingAll = false

    var body: some View {
        let layout = BulletinLayout(bulletin: report.bulletin)
        let tree = IntensityObservationTree(sections: layout.intensitySections)
        let depth = tree.nodes(atDepth: 2).contains(where: { $0.longPeriodText != nil }) ? 2 : 1
        Section {
            ForEach(Array(layout.longPeriodRows.enumerated()), id: \.offset) { _, row in
                KeyValueRow(label: row.label, value: row.value, emphasized: true)
            }
            ForEach(tree.longPeriodGroups(atDepth: depth)) { group in
                HStack(alignment: .top, spacing: 10) {
                    LongPeriodBadge(level: group.level, text: group.id)
                    ExpandableNameList(names: group.nodes.map(\.name))
                }
            }
            if !tree.isEmpty {
                Button {
                    showingAll = true
                } label: {
                    Label("観測点ごとの階級・絶対速度応答（\(tree.leaves.count)件）", systemImage: "list.bullet.indent")
                        .font(.subheadline)
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("longPeriodObservations")
                .sheet(isPresented: $showingAll) {
                    IntensityObservationsView(tree: tree, title: "長周期地震動の観測")
                        .presentationDetents([.medium, .large])
                        .presentationDragIndicator(.visible)
                }
            }
        } header: {
            Text("長周期地震動")
        } footer: {
            Text((TelegramCatalog.label(for: report)) + " · " + DisplayTime.full(report.occurredAt ?? report.receivedAt) + " 発表")
        }
    }
}

/// Full observation list: by area (default) or by intensity class.
struct IntensityObservationsView: View {
    let tree: IntensityObservationTree
    var title = "各地の震度"
    @Environment(\.dismiss) private var dismiss
    @State private var mode = 0

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Picker("表示", selection: $mode) {
                        Text("地域別").tag(0)
                        Text("震度別").tag(1)
                    }
                    .pickerStyle(.segmented)
                    .disabled(!tree.hasSeismicIntensity)
                }
                if mode == 0 || !tree.hasSeismicIntensity {
                    byArea
                } else {
                    byIntensity
                }
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("閉じる") { dismiss() }
                        .accessibilityIdentifier("closeIntensityObservations")
                }
            }
        }
    }

    @ViewBuilder
    private var byArea: some View {
        ForEach(tree.listSections) { anchor in
            Section {
                let descendants = anchor.descendants
                if descendants.isEmpty {
                    IntensityNodeRow(node: anchor, indent: 0)
                }
                ForEach(descendants) { node in
                    IntensityNodeRow(node: node, indent: max(0, node.depth - anchor.depth - 1))
                }
            } header: {
                HStack(spacing: 8) {
                    Text(anchor.path.joined(separator: " "))
                    Spacer()
                    if let intensity = anchor.intensity {
                        Text("最大 " + intensity.displayText).monospacedDigit()
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var byIntensity: some View {
        ForEach(tree.leafGroups) { group in
            Section {
                ForEach(group.nodes) { node in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(node.name).font(.subheadline)
                        Text(node.parentPath.joined(separator: " ")).font(.caption).foregroundStyle(.secondary)
                    }
                }
            } header: {
                HStack(spacing: 8) {
                    IntensityBadge(reading: group.reading, size: 22)
                    Text("震度\(group.reading?.displayText ?? "記載なし")（\(group.nodes.count)件）")
                }
            }
        }
    }
}

private struct IntensityNodeRow: View {
    let node: IntensityNode
    let indent: Int

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
            if indent > 0 {
                Color.clear.frame(width: CGFloat(indent) * 16, height: 1)
            }
            IntensityBadge(reading: node.intensity, size: indent > 0 ? 24 : 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(node.name)
                    .font(indent > 0 ? .subheadline : .subheadline.weight(.semibold))
                if node.svaText != nil || !node.otherRows.isEmpty {
                    Text(details).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 4)
            if let text = node.longPeriodText {
                LongPeriodBadge(level: node.longPeriod, text: text)
            }
        }
    }

    private var details: String {
        var parts: [String] = []
        if let sva = node.svaText { parts.append("絶対速度応答 " + sva) }
        for row in node.otherRows { parts.append(row.label + " " + row.value) }
        return parts.joined(separator: " · ")
    }
}

// MARK: - Texts

/// 防災上の留意事項 and other fixed comments of a bulletin.
struct BulletinCommentsSection: View {
    let report: ReportEntity

    var body: some View {
        let comments = BulletinLayout(bulletin: report.bulletin).comments
        if !comments.isEmpty {
            Section {
                ForEach(comments) { item in
                    NoticeBox(text: item.text, title: item.role.rawValue,
                              systemImage: item.role == .warningComment ? "exclamationmark.bubble" : "text.bubble",
                              color: item.role == .warningComment ? .blue : .secondary)
                }
            } header: {
                Text("付加文・留意事項")
            } footer: {
                Text(TelegramCatalog.label(for: report))
            }
        }
    }
}

/// Headline, non-comment texts and unrecognised sections of one report.
struct BulletinRemainderSection: View {
    let report: ReportEntity

    static func hasContent(_ report: ReportEntity) -> Bool {
        let layout = BulletinLayout(bulletin: report.bulletin)
        return layout.headline != nil || layout.textItems.contains { !$0.role.isComment } || !layout.otherSections.isEmpty
    }

    var body: some View {
        let layout = BulletinLayout(bulletin: report.bulletin)
        Section {
            if let headline = layout.headline {
                TitledText(title: "見出し", text: headline)
            }
            ForEach(layout.textItems.filter { !$0.role.isComment }) { item in
                TitledText(title: item.role.rawValue, text: item.text)
            }
            ForEach(Array(layout.otherSections.enumerated()), id: \.offset) { _, section in
                GenericSectionView(section: section)
            }
        } header: {
            Text(TelegramCatalog.name(report.telegramType) ?? report.title)
        }
    }
}

/// Fallback rendering for any bulletin section.
struct GenericSectionView: View {
    let section: BulletinSectionDTO

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(section.title).font(.subheadline.weight(.semibold))
            if let text = section.text, !text.isEmpty {
                Text(text).font(.subheadline).textSelection(.enabled)
            }
            ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                KeyValueRow(label: row.label, value: row.value)
            }
        }
        .padding(.vertical, 2)
    }
}
