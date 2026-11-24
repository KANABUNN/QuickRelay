import SwiftData
import SwiftUI

struct EventListView: View {
    var category = "earthquake"
    private var title: String {
        switch category { case "tsunami": "津波"; case "advisory": "関連情報"; default: "地震" }
    }
    private var categoryEvents: [EventEntity] { events.filter { $0.category == category } }
    @EnvironmentObject private var history: HistoryPreferences
    @EnvironmentObject private var environment: AppEnvironment
    @State private var filter = HistoryFilter()
    @State private var showingFilters = false
    @State private var searching = false
    @Query private var reports: [ReportEntity]
    private var scope: String { repository.serverIdentity.isEmpty ? environment.settings.serverBaseURL : repository.serverIdentity }
    private var groupedReports: [String: [ReportEntity]] { Dictionary(grouping: reports, by: \.eventId) }
    private var visibleEvents: [EventEntity] {
        let groups = groupedReports
        return categoryEvents.filter { event in
            let eventReports = groups[event.id] ?? []
            let searchable = searchText(for: event, reports: eventReports)
            return filter.matches(event, searchText: searchable, unread: history.isUnread(event, scope: scope),
                                  pinned: history.isPinned(event.id, scope: scope),
                                  maxIntensity: filterIntensity(for: event, reports: eventReports))
        }.sorted {
            let a = history.isPinned($0.id, scope: scope), b = history.isPinned($1.id, scope: scope)
            if a != b { return a }
            if $0.latestReportAt != $1.latestReportAt { return $0.latestReportAt > $1.latestReportAt }
            return $0.id < $1.id
        }
    }
    private func searchText(for event: EventEntity, reports: [ReportEntity]) -> String {
        guard !filter.search.isEmpty else { return "" }
        var values: [String] = [event.numericHypocenter.epicenter ?? "", event.displayTitle]
        for report in reports {
            values.append(report.title)
            values.append(report.body)
            for area in report.affectedAreas ?? [] { values.append(area.name) }
            for section in report.bulletin?.sections ?? [] {
                values.append(section.title)
                if let text = section.text { values.append(text) }
                for row in section.rows ?? [] { values.append(row.value) }
            }
        }
        return values.joined(separator: " ")
    }
    /// The same value as the row badge, so a later 震源要素更新 does not hide the event.
    private func filterIntensity(for event: EventEntity, reports: [ReportEntity]) -> String? {
        guard !filter.minimumIntensity.isEmpty, event.category == "earthquake" else { return event.maxIntensity }
        return EarthquakeIntensitySummary(event: event, reports: reports).display?.text
    }

    @EnvironmentObject private var repository: EventRepository
    @Query(sort: \EventEntity.latestReportAt, order: .reverse) private var events: [EventEntity]

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            VStack(spacing: 0) {
                ReceiverStatusView()
                if filter.isActive {
                    HStack {
                        Text("絞り込み結果 \(visibleEvents.count)件").font(.caption)
                        Spacer()
                        Button("解除") { filter = HistoryFilter() }.font(.caption)
                    }.padding(.horizontal).padding(.vertical, 5)
                }
                listContent(now: context.date)
            }
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .searchable(text: $filter.search, isPresented: $searching, prompt: "震源名・地域・発表内容を検索")
        .onDisappear { searching = false }
        .sheet(isPresented: $showingFilters) {
            HistoryFilterView(filter: $filter, category: category)
                .presentationDetents([.large])
        }
        .safeAreaInset(edge: .bottom) {
            if let error = repository.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
                    .padding(8)
                    .frame(maxWidth: .infinity)
                    .background(.thinMaterial)
            }
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button("履歴を絞り込む", systemImage: filter.isActive ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle") {
                    showingFilters = true
                }.accessibilityIdentifier("historyFilters")
            }
            ToolbarItem(placement: .topBarTrailing) {
                if repository.isSyncing {
                    ProgressView()
                } else {
                    Button("同期", systemImage: "arrow.clockwise") {
                        Task { _ = await repository.syncAll() }
                    }
                }
            }
        }
    }

    private func listContent(now: Date) -> some View {
        let groups = groupedReports
        let index = EventContinuationIndex(reports: reports)
        let ongoing = categoryEvents.filter { index.status(for: $0, now: now).isOngoing }
            .sorted { $0.latestReportAt > $1.latestReportAt }
        return Group {
            if visibleEvents.isEmpty && ongoing.isEmpty {
                ContentUnavailableView(
                    filter.isActive ? "条件に合う履歴はありません" : "\(title)の受信履歴はありません",
                    systemImage: "waveform.path.ecg",
                    description: Text(filter.isActive ? "検索語や絞り込み条件を変更してください。" : "下へ引いてサーバーと同期できます。")
                )
            } else {
                List {
                    if !ongoing.isEmpty {
                        Section {
                            ForEach(ongoing) { event in
                                eventLink(event, groups: groups, continuation: index.status(for: event, now: now))
                                    .id("ongoing-" + event.id)
                            }
                        } header: {
                            Text("発表中")
                        } footer: {
                            Text("受信した最新発表に基づきます。接続状態と同期時刻も確認してください。")
                        }
                        .accessibilityIdentifier("ongoingEvents")
                    }
                    Section {
                        if visibleEvents.isEmpty {
                            Text("条件に合う履歴はありません").foregroundStyle(.secondary)
                        }
                        ForEach(visibleEvents) { event in
                            eventLink(event, groups: groups, continuation: index.status(for: event, now: now))
                                .id("history-" + event.id)
                        }
                    } header: {
                        Text("履歴（\(visibleEvents.count)件）")
                    }
                    .accessibilityIdentifier("eventHistory")
                }
                .listStyle(.plain)
            }
        }
        .refreshable { _ = await repository.syncAll() }
    }

    private func eventLink(_ event: EventEntity, groups: [String: [ReportEntity]], continuation: EventContinuation) -> some View {
        NavigationLink(value: AppRoute.event(id: event.id, reportID: nil)) {
            EventRow(event: event, reports: groups[event.id] ?? [], unread: history.isUnread(event, scope: scope),
                     pinned: history.isPinned(event.id, scope: scope), continuation: continuation)
        }
        .swipeActions(edge: .leading) {
            Button(history.isPinned(event.id, scope: scope) ? "ピンを外す" : "ピン留め", systemImage: "pin") {
                history.togglePinned(event.id, scope: scope)
            }.tint(.blue)
        }
        .swipeActions(edge: .trailing) {
            Button("既読にする", systemImage: "checkmark") { history.markRead(event, scope: scope) }.tint(.gray)
        }
        .contextMenu {
            Button(history.isPinned(event.id, scope: scope) ? "ピンを外す" : "ピン留め", systemImage: "pin") {
                history.togglePinned(event.id, scope: scope)
            }
            Button("既読にする", systemImage: "checkmark") { history.markRead(event, scope: scope) }
        }
    }
}

// MARK: - Rows

/// One history row; the layout depends on the event category.
struct EventRow: View {
    let event: EventEntity
    var reports: [ReportEntity] = []
    var unread = false
    var pinned = false
    var continuation = EventContinuation.history

    var body: some View {
        switch event.category {
        case "tsunami":
            TsunamiEventRow(event: event, reports: reports, unread: unread, pinned: pinned, continuation: continuation)
        case "earthquake":
            EarthquakeEventRow(event: event, reports: reports, unread: unread, pinned: pinned, continuation: continuation)
        default:
            AdvisoryEventRow(event: event, reports: reports, unread: unread, pinned: pinned)
        }
    }
}

private struct RowMarks: View {
    let unread: Bool
    let pinned: Bool

    var body: some View {
        if unread {
            Circle().fill(Color.accentColor).frame(width: 7, height: 7).accessibilityLabel("未読")
        }
        if pinned {
            Image(systemName: "pin.fill").font(.caption).foregroundStyle(.blue).accessibilityLabel("ピン留め")
        }
    }
}

private struct EarthquakeEventRow: View {
    let event: EventEntity
    let reports: [ReportEntity]
    let unread: Bool
    let pinned: Bool
    let continuation: EventContinuation

    var body: some View {
        let hypocenter = event.numericHypocenter
        // From all reports of the earthquake: the event row mirrors only the latest
        // publication, and 震源要素更新 (VXSE61) has no intensity.
        let intensity = EarthquakeIntensitySummary(event: event, reports: reports)
        HStack(alignment: .top, spacing: 12) {
            IntensityBadge(reading: intensity.display, size: 48,
                           caption: intensity.isForecast ? "予想" : nil,
                           longPeriodText: intensity.longPeriodText)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 5) {
                    RowMarks(unread: unread, pinned: pinned)
                    Text(hypocenter.epicenter ?? event.displayTitle)
                        .font(.headline)
                        .lineLimit(2)
                    Spacer(minLength: 4)
                    Text(DisplayTime.dayTime(ValueText.originTime(hypocenter) ?? event.latestReportAt))
                        .font(.subheadline.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Text(metrics(hypocenter))
                    .font(.subheadline.monospacedDigit())
                HStack(spacing: 5) {
                    Text(kindLabel).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    if continuation.isOngoing { StatusBadge(text: "継続中", color: .orange, filled: true) }
                    if event.isCancelled { StatusBadge(text: "取消", color: .orange) }
                    else if event.isEEW && event.isFinal { StatusBadge(text: "最終", color: .blue) }
                    if hypocenter.isAssumed { StatusBadge(text: "仮定震源", color: .orange) }
                    if hypocenter.status == "low_accuracy" { StatusBadge(text: "低精度", color: .orange) }
                }
                if continuation.isOngoing {
                    Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
                } else if continuation.state == .unknown {
                    Text("継続状況未確認").font(.caption.bold()).foregroundStyle(.orange)
                }
            }
        }
        .padding(.vertical, 3)
    }

    private var kindLabel: String {
        if event.isEEW {
            return (RelayEventType(rawValue: event.eventType)?.displayName ?? event.displayTitle) + " " + event.publicationLabel
        }
        return event.displayTitle + " · " + event.publicationLabel
    }

    private func metrics(_ value: HypocenterDTO) -> String {
        if event.isWarningProduct { return "M・深さは表示しません（警報）" }
        var parts: [String] = []
        if let magnitude = ValueText.magnitude(value) { parts.append(magnitude) }
        if let depth = value.depthText { parts.append("深さ " + depth) }
        guard !parts.isEmpty else { return event.isCancelled ? "取り消された発表" : "震源要素の記載なし" }
        return parts.joined(separator: "　") + (value.isAssumed ? "（仮定値）" : "")
    }
}

private struct TsunamiEventRow: View {
    let event: EventEntity
    let reports: [ReportEntity]
    let unread: Bool
    let pinned: Bool
    let continuation: EventContinuation

    var body: some View {
        let latest = TsunamiPublication.latest(in: reports, eventID: event.id)
        let publication = latest.map { TsunamiPublication(report: $0) }
        let warnings = activeWarnings(publication)
        let top = warnings.first.map { TsunamiKindStyle(kind: $0.level.title) }
        VStack(alignment: .leading, spacing: 5) {
            HStack(alignment: .firstTextBaseline, spacing: 5) {
                RowMarks(unread: unread, pinned: pinned)
                Text(latest?.title ?? event.displayTitle)
                    .font(.headline)
                    .lineLimit(2)
                Spacer(minLength: 4)
                Text(DisplayTime.dayTime(latest.map { $0.occurredAt ?? $0.receivedAt } ?? event.latestReportAt))
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            if !warnings.isEmpty {
                ForEach(warnings) { warning in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        TsunamiKindTag(style: TsunamiKindStyle(kind: warning.level.title), text: warning.level.title)
                        Text(areaSummary(warning.areas.map(\.name)))
                            .font(.caption)
                            .lineLimit(2)
                    }
                }
            } else if let status = publication?.status {
                Label(status, systemImage: latest?.isCancelled == true ? "xmark.octagon" : "water.waves")
                    .font(.caption.bold())
                    .foregroundStyle(latest?.isCancelled == true ? .orange : .blue)
            }
            if let line = observationLine {
                Text(line).font(.caption.monospacedDigit()).lineLimit(2)
            }
            HStack(spacing: 5) {
                Text(TelegramCatalog.short(latest?.telegramType) ?? event.displayTitle)
                    .font(.caption).foregroundStyle(.secondary)
                Text(latest?.publicationLabel ?? event.publicationLabel).font(.caption).foregroundStyle(.secondary)
                if continuation.isOngoing { StatusBadge(text: "継続中", color: .orange, filled: true) }
                if latest?.isCancelled == true { StatusBadge(text: "取消", color: .orange) }
            }
            if continuation.isOngoing && warnings.isEmpty {
                Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
            } else if continuation.state == .unknown {
                Text("継続状況未確認").font(.caption.bold()).foregroundStyle(.orange)
            }
        }
        .leadingBar(top?.fill ?? Color.secondary.opacity(0.25), width: 5)
        .padding(.vertical, 3)
    }

    private func activeWarnings(_ publication: TsunamiPublication?) -> [TsunamiPublication.WarningGroup] {
        guard let publication, continuation.reportIDs.contains(publication.report.id) else { return [] }
        return publication.warnings
    }

    private func areaSummary(_ names: [String]) -> String {
        let shown = names.prefix(3).joined(separator: "、")
        return names.count > 3 ? shown + " ほか\(names.count - 3)予報区" : shown
    }

    /// Most notable observed or estimated wave of the event, when ongoing.
    private var observationLine: String? {
        guard continuation.isOngoing,
              let group = TsunamiObservation.groups(in: reports).max(by: {
                  ReportPublicationOrder.areInAscendingOrder($0.report, $1.report)
              }),
              let point = group.observations.first else { return nil }
        var parts = [group.kind.title + " " + point.name]
        if let maximum = point.maximumWave { parts.append("最大波 " + maximum) }
        if let first = point.firstWave { parts.append("第1波 " + first) }
        return parts.joined(separator: " · ")
    }
}

private struct AdvisoryEventRow: View {
    let event: EventEntity
    let reports: [ReportEntity]
    let unread: Bool
    let pinned: Bool

    var body: some View {
        let report = reports.first { $0.serverSequence == Int64(event.latestRevision ?? -1) } ?? reports.last
        let layout = BulletinLayout(bulletin: report?.bulletin)
        let kind = layout.text(.infoKind)
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 5) {
                RowMarks(unread: unread, pinned: pinned)
                Text(event.displayTitle)
                    .font(.headline)
                    .lineLimit(2)
                Spacer(minLength: 4)
                Text(DisplayTime.dayTime(event.latestReportAt))
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            if let kind {
                Text(kind)
                    .font(.caption.bold())
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .foregroundStyle(AdvisoryStyle.foreground(kind))
                    .background(AdvisoryStyle.color(kind), in: RoundedRectangle(cornerRadius: 4, style: .continuous))
            }
            if let summary = report?.body, !summary.isEmpty {
                Text(summary).font(.caption).lineLimit(3)
            }
            HStack(spacing: 5) {
                Text(TelegramCatalog.short(report?.telegramType) ?? "")
                    .font(.caption).foregroundStyle(.secondary)
                Text(event.publicationLabel).font(.caption).foregroundStyle(.secondary)
                if event.isCancelled { StatusBadge(text: "取消", color: .orange) }
            }
        }
        .leadingBar(kind.map(AdvisoryStyle.color) ?? Color.purple.opacity(0.5), width: 5)
        .padding(.vertical, 3)
    }
}
