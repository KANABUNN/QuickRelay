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
            let searchable = searchText(for: event, reports: groups[event.id] ?? [])
            return filter.matches(event, searchText: searchable, unread: history.isUnread(event, scope: scope),
                                  pinned: history.isPinned(event.id, scope: scope))
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
            for section in report.bulletin?.sections ?? [] {
                values.append(section.title)
                if let text = section.text { values.append(text) }
                for row in section.rows ?? [] { values.append(row.value) }
            }
        }
        return values.joined(separator: " ")
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
                    Section("履歴") {
                        if visibleEvents.isEmpty {
                            Text("条件に合う履歴はありません").foregroundStyle(.secondary)
                        }
                        ForEach(visibleEvents) { event in
                            eventLink(event, groups: groups, continuation: index.status(for: event, now: now))
                                .id("history-" + event.id)
                        }
                    }
                    .accessibilityIdentifier("eventHistory")
                }
                .listStyle(.plain)
            }
        }
        .refreshable { _ = await repository.syncAll() }
    }

    private func eventLink(_ event: EventEntity, groups: [String: [ReportEntity]], continuation: EventContinuation) -> some View {
        let reports = groups[event.id] ?? []
        let tsunamiReport = TsunamiPublication.latest(in: reports, eventID: event.id)
        let observation = tsunamiReport != nil && continuation.isOngoing ?
            TsunamiObservation.groups(in: reports).max {
                ReportPublicationOrder.areInAscendingOrder($0.report, $1.report)
            } : nil
        return NavigationLink(value: AppRoute.event(id: event.id, reportID: nil)) {
            EventRow(event: event, unread: history.isUnread(event, scope: scope),
                     pinned: history.isPinned(event.id, scope: scope),
                     tsunamiReport: tsunamiReport, tsunamiObservation: observation,
                     continuation: continuation)
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

private struct EventRow: View {
    let event: EventEntity
    var unread = false
    var pinned = false
    var tsunamiReport: ReportEntity?
    var tsunamiObservation: TsunamiObservation.Group?
    var continuation = EventContinuation.history

    private var activeTsunamiWarning: TsunamiPublication? {
        guard let report = tsunamiReport, continuation.reportIDs.contains(report.id) else { return nil }
        let publication = TsunamiPublication(report: report)
        return publication.warnings.isEmpty ? nil : publication
    }

    var body: some View {
        let warning = activeTsunamiWarning
        let headline = warning?.report.title ?? event.displayTitle
        VStack(alignment: .leading, spacing: 6) {
            if let warning {
                TsunamiWarningSummary(publication: warning, compact: true)
            }
            if let group = tsunamiObservation, let point = group.observations.first {
                Text("\(group.kind.title) \(point.name)").font(.subheadline.bold())
                if let maximum = point.maximumWave {
                    Text("最大波 \(maximum)").font(.caption)
                }
                if let first = point.firstWave {
                    Text("第1波 \(first)").font(.caption)
                }
            }
            HStack(alignment: .firstTextBaseline) {
                if unread { Circle().fill(Color.accentColor).frame(width: 7, height: 7).accessibilityLabel("未読") }
                if pinned { Image(systemName: "pin.fill").font(.caption).foregroundStyle(.blue).accessibilityLabel("ピン留め") }
                Text(event.category == "earthquake" ? (event.numericHypocenter.epicenter ?? headline) : headline)
                    .font(warning == nil ? .headline : .subheadline)
                if event.numericHypocenter.isAssumed {
                    StatusBadge(text: "仮定震源", color: .orange)
                }
                Spacer()
                Text(event.latestReportAt, format: .dateTime.month().day().hour().minute())
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(.secondary)
            }

            if continuation.isOngoing {
                Label("継続中", systemImage: "dot.radiowaves.left.and.right")
                    .font(.caption.bold()).foregroundStyle(.orange)
                    .accessibilityIdentifier("ongoing-" + event.id)
                if warning == nil { Text(continuation.explanation).font(.caption).foregroundStyle(.secondary) }
            } else if continuation.state == .unknown {
                Text("継続状況未確認").font(.caption.bold()).foregroundStyle(.orange)
            }

            HStack(spacing: 10) {
                if let intensity = event.maxIntensity {
                    Label("\(event.intensityLabel) \(intensity)", systemImage: "gauge.with.dots.needle.67percent")
                }
                if !event.isWarningProduct, let magnitude = event.numericHypocenter.magnitude {
                    Text("M\(magnitude.formatted(.number.precision(.fractionLength(1))))\(event.numericHypocenter.isAssumed ? "（仮定値）" : "")")
                }
            }
            .font(.subheadline)

            if warning == nil, let report = tsunamiReport, let status = TsunamiPublication(report: report).status {
                Label(status, systemImage: report.isCancelled ? "xmark.octagon" : "water.waves")
                    .font(.caption.bold()).foregroundStyle(report.isCancelled ? .orange : .blue)
                Text("警報・注意報の発表 \((report.occurredAt ?? report.receivedAt).formatted(date: .abbreviated, time: .shortened))")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            if let note = event.numericHypocenter.qualification {
                Text(note).font(.caption).foregroundStyle(.orange)
            }

            HStack(spacing: 8) {
                if event.category != "tsunami" {
                    Text(event.isEEW ? (RelayEventType(rawValue: event.eventType)?.displayName ?? event.displayTitle) : event.displayTitle)
                }
                Text(warning?.report.publicationLabel ?? event.publicationLabel)
                if warning == nil && event.isCancelled {
                    StatusBadge(text: "取消", color: .orange)
                } else if event.isEEW && event.isFinal {
                    StatusBadge(text: "最終", color: .blue)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
    }
}

struct TsunamiWarningSummary: View {
    let publication: TsunamiPublication
    var compact = false

    var body: some View {
        let warnings = publication.warnings
        VStack(alignment: .leading, spacing: 8) {
            ForEach(warnings) { warning in
                VStack(alignment: .leading, spacing: 3) {
                    Label(warning.level.title, systemImage: "exclamationmark.triangle.fill")
                        .font(warning.level == warnings.first?.level ? .headline : .subheadline.bold())
                        .foregroundStyle(color(for: warning.level))
                    let areas = warning.areas.prefix(3)
                    let remaining = warning.areas.count - areas.count
                    if remaining > 0 && !compact {
                        DisclosureGroup(areaNames(areas) + " ほか\(remaining)予報区") {
                            Text(areaNames(warning.areas.dropFirst(areas.count)))
                        }
                        .font(.subheadline)
                    } else {
                        Text(areaNames(areas) + (remaining > 0 ? " ほか\(remaining)予報区" : ""))
                            .font(compact ? .caption : .subheadline)
                    }
                }
            }
            LabeledContent("警報・注意報の発表時刻", value: (publication.report.occurredAt ?? publication.report.receivedAt)
                .formatted(date: .abbreviated, time: compact ? .shortened : .standard))
                .font(.caption)
                .foregroundStyle(.secondary)
            if publication.areas.contains(where: \.withdrawn) {
                Text("一部の予報区で解除").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func areaNames(_ areas: ArraySlice<TsunamiPublication.Area>) -> String {
        areas.map { area in
            guard !compact, let height = area.height else { return area.name }
            return "\(area.name)（予想される高さ \(height)）"
        }.joined(separator: "、")
    }

    private func color(for level: TsunamiPublication.WarningLevel) -> Color {
        switch level {
        case .major: .purple
        case .warning: .red
        case .advisory: .orange
        }
    }
}

struct StatusBadge: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.caption2.bold())
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(color)
            .background(color.opacity(0.12), in: Capsule())
    }
}
