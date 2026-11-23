import Foundation
import SwiftData
import SwiftUI
import UniformTypeIdentifiers

struct EventDetailView: View {
    let eventID: String
    let highlightedReportID: String?

    @EnvironmentObject private var history: HistoryPreferences
    @EnvironmentObject private var environment: AppEnvironment
    @Environment(\.scenePhase) private var scenePhase
    @State private var showingMap = false
    @State private var isVisible = false
    private var scope: String { repository.serverIdentity.isEmpty ? environment.settings.serverBaseURL : repository.serverIdentity }
    @EnvironmentObject private var repository: EventRepository
    @Query private var events: [EventEntity]
    @Query private var reports: [ReportEntity]
    @Query private var allReports: [ReportEntity]

    init(eventID: String, highlightedReportID: String?) {
        self.eventID = eventID
        self.highlightedReportID = highlightedReportID
        let requestedEventID = eventID
        _events = Query(
            filter: #Predicate<EventEntity> { $0.id == requestedEventID }
        )
        _reports = Query(
            filter: #Predicate<ReportEntity> { $0.eventId == requestedEventID },
            sort: [SortDescriptor(\ReportEntity.serverSequence, order: .forward)]
        )
    }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            Group {
                if let event = events.first {
                    let continuation = EventContinuationIndex(reports: allReports).status(for: event, now: context.date)
                    let publication = tsunamiPublication
                    let showsActiveWarning = publication.map {
                        continuation.reportIDs.contains($0.report.id) && !$0.warnings.isEmpty
                    } == true
                    let observations = tsunamiObservations
                    ScrollViewReader { proxy in
                        List {
                            if let publication {
                                tsunamiWarningSection(publication, isActive: showsActiveWarning,
                                                      isHistorical: continuation.state == .history)
                            }
                            ForEach(observations, id: \.kind.title) { group in
                                TsunamiObservationSection(group: group, highlighted: group.report.id == highlightedReportID)
                            }
                            latestSection(event, continuation: continuation, showsActiveWarning: showsActiveWarning)
                            if let latestReport {
                                ReportChangesView(report: latestReport, previous: ReportComparison.previous(to: latestReport, in: reports))
                            }
                            if event.isWarningProduct && !event.isCancelled { forecastReferenceSection }
                            if let latestReport, let bulletin = latestReport.bulletin {
                                let summarized = observations.filter { $0.report.id == latestReport.id }
                                    .flatMap { $0.observations.map(\.section) }
                                Section("発表内容") {
                                    BulletinContent(bulletin: bulletin, reportID: latestReport.id,
                                                    summarizedSections: summarized)
                                }
                            }
                            timelineSection(continuation: continuation)
                        }
                        .onAppear { scrollToHighlightedReport(using: proxy) }
                        .onChange(of: reports.count) { _, _ in
                            scrollToHighlightedReport(using: proxy)
                        }
                    }
                } else {
                    ContentUnavailableView(
                        "情報を読み込めません",
                        systemImage: "exclamationmark.icloud",
                        description: Text("同期後も見つからない場合は、対象の情報がサーバーに残っているか確認してください。")
                    )
                }
            }
        }
        .navigationTitle(tsunamiPublication?.report.title ?? events.first?.displayTitle ?? "情報の詳細")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button(history.isPinned(eventID, scope: scope) ? "ピンを外す" : "ピン留め", systemImage: history.isPinned(eventID, scope: scope) ? "pin.fill" : "pin") {
                    history.togglePinned(eventID, scope: scope)
                }.accessibilityIdentifier("pinEvent")
            }
            if events.first?.category == "earthquake" || events.first?.category == "tsunami" {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("地図で見る", systemImage: "map") { showingMap = true }
                        .disabled(latestReport == nil).accessibilityIdentifier("showEventMap")
                }
            }
        }
        .sheet(isPresented: $showingMap) {
            if let latestReport { EventMapView(report: latestReport).presentationDetents([.large]) }
        }
        .onAppear { isVisible = true; markReadIfVisible() }
        .onDisappear { isVisible = false }
        .onChange(of: events.first?.latestRevision) { _, _ in markReadIfVisible() }
        .onChange(of: scenePhase) { _, _ in markReadIfVisible() }
        .refreshable {
            _ = await repository.syncAll()
        }
        .task {
            if events.isEmpty {
                _ = await repository.syncAll()
            }
        }
    }

    @ViewBuilder
    private func tsunamiWarningSection(_ publication: TsunamiPublication, isActive: Bool, isHistorical: Bool) -> some View {
        if isActive {
            Section("発表中") {
                TsunamiWarningSummary(publication: publication)
                    .accessibilityElement(children: .contain)
                    .accessibilityIdentifier("detailOngoing")
            }
        } else if let status = publication.status {
            Section(isHistorical ? "過去の警報・注意報" : "警報・注意報の発表") {
                Label(status, systemImage: publication.report.isCancelled ? "xmark.octagon" : "water.waves")
                    .font(.subheadline.bold()).foregroundStyle(publication.report.isCancelled ? .orange : .blue)
                LabeledContent("発表時刻", value: (publication.report.occurredAt ?? publication.report.receivedAt)
                    .formatted(date: .abbreviated, time: .standard))
            }
        }
    }

    @ViewBuilder
    private func latestSection(_ event: EventEntity, continuation: EventContinuation, showsActiveWarning: Bool) -> some View {
        Section("最新状態") {
            if continuation.isOngoing && !showsActiveWarning {
                Label("継続中", systemImage: "dot.radiowaves.left.and.right")
                    .font(.headline).foregroundStyle(.orange)
                    .accessibilityIdentifier("detailOngoing")
                Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
            } else if continuation.state == .unknown {
                Label("継続状況未確認", systemImage: "exclamationmark.circle").foregroundStyle(.orange)
                Text(continuation.explanation).font(.caption)
            } else if continuation.state == .ended {
                Text(continuation.explanation).font(.caption).foregroundStyle(.secondary)
            }
            LabeledContent("種別", value: RelayEventType(rawValue: event.eventType)?.displayName ?? event.eventType)
            LabeledContent("発表時刻", value: event.latestReportAt.formatted(date: .abbreviated, time: .standard))
            HypocenterFields(value: event.numericHypocenter, showMagnitudeAndDepth: !event.isWarningProduct)
            if let intensity = event.maxIntensity {
                LabeledContent(event.intensityLabel, value: intensity)
            }
            LabeledContent(event.isEEW ? "最新報" : "発表区分", value: event.publicationLabel)
            if let latestReport {
                VStack(alignment: .leading, spacing: 6) {
                    Text(latestReport.title)
                        .font(.subheadline.bold())
                    Text(latestReport.body)
                        .font(.subheadline)
                        .textSelection(.enabled)
                }
            }
            if event.isCancelled {
                Label(event.category == "tsunami" ? "最新の電文は取り消されました" : "この情報は取り消されました",
                      systemImage: "xmark.octagon.fill")
                    .foregroundStyle(.orange)
            } else if event.isEEW && event.isFinal {
                Label("最終報", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.blue)
            }
        }
    }

    @ViewBuilder
    private var forecastReferenceSection: some View {
        Section("同じ地震の予報値（参考）") {
            if let forecast = ForecastReference.latest(eventID: eventID, reports: reports) {
                LabeledContent("情報の出所", value: forecast.telegramType == "VXSE45"
                    ? "緊急地震速報（地震動予報 / VXSE45）"
                    : forecast.telegramType == "VXSE44"
                        ? "緊急地震速報（予報 / VXSE44）" : "緊急地震速報（予報）")
                if let revision = forecast.revision { LabeledContent("予報の報数", value: "第\(revision)報") }
                LabeledContent("予報の発表時刻", value: (forecast.occurredAt ?? forecast.receivedAt).formatted(date: .abbreviated, time: .standard))
                if let value = forecast.numericHypocenter {
                    HypocenterFields(value: value)
                } else {
                    Text(forecast.body).textSelection(.enabled)
                }
                if let intensity = forecast.maxIntensity { LabeledContent("予想最大震度", value: intensity) }
                Text("同じ地震の予報から取得した値です。警報とは発表時刻が異なる場合があります。")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                Text("表示できる予報の数値はありません。マグニチュード・深さは予報を受信すると表示します。")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func timelineSection(continuation: EventContinuation) -> some View {
        Section("発表履歴") {
            if reports.isEmpty {
                Text("発表履歴はまだ同期されていません。")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(sortedReports) { report in
                    ReportTimelineRow(
                        report: report,
                        highlighted: report.id == highlightedReportID,
                        ongoing: continuation.reportIDs.contains(report.id)
                    )
                    .id(report.id)
                }
            }
        }
    }

    private var tsunamiPublication: TsunamiPublication? {
        guard events.first?.category == "tsunami",
              let report = TsunamiPublication.latest(in: reports, eventID: eventID) else { return nil }
        return TsunamiPublication(report: report)
    }

    private var tsunamiObservations: [TsunamiObservation.Group] {
        guard events.first?.category == "tsunami" else { return [] }
        let groups = TsunamiObservation.groups(in: reports)
        guard let highlightedReportID else { return groups }
        return groups.filter { $0.report.id == highlightedReportID } + groups.filter { $0.report.id != highlightedReportID }
    }

    private var sortedReports: [ReportEntity] {
        ReportTimelineDisplay.visible(reports).sorted(by: ReportTimelineOrder.areInAscendingOrder)
    }

    private var latestReport: ReportEntity? {
        guard let version = events.first?.latestRevision else { return nil }
        return reports.first { $0.serverSequence == Int64(version) }
    }

    private func markReadIfVisible() {
        guard isVisible, scenePhase == .active, let event = events.first else { return }
        history.markRead(event, scope: scope)
    }

    private func scrollToHighlightedReport(using proxy: ScrollViewProxy) {
        guard let highlightedReportID else { return }
        if let publication = tsunamiPublication, publication.report.id == highlightedReportID, publication.status != nil {
            return
        }
        if tsunamiObservations.contains(where: { $0.report.id == highlightedReportID }) { return }
        DispatchQueue.main.async {
            withAnimation { proxy.scrollTo(highlightedReportID, anchor: .center) }
        }
    }
}

private struct TsunamiObservationSection: View {
    let group: TsunamiObservation.Group
    let highlighted: Bool

    var body: some View {
        Section(group.kind.title) {
            LabeledContent("発表時刻", value: (group.report.occurredAt ?? group.report.receivedAt)
                .formatted(date: .abbreviated, time: .standard))
                .font(.caption)
                .foregroundStyle(.secondary)
            ForEach(Array(group.observations.prefix(3).enumerated()), id: \.offset) { _, observation in
                VStack(alignment: .leading, spacing: 4) {
                    Text(observation.name).font(.subheadline.bold())
                    if let first = observation.firstWave { LabeledContent("第1波", value: first) }
                    if let maximum = observation.maximumWave { LabeledContent("最大波", value: maximum) }
                }
                .font(.subheadline)
            }
            DisclosureGroup(group.kind == .estimate ? "推定の詳細（\(group.observations.count)予報区）"
                                                    : "観測の詳細（\(group.observations.count)地点）") {
                ForEach(Array(group.observations.enumerated()), id: \.offset) { _, observation in
                    BulletinSectionContent(section: observation.section)
                }
            }
        }
        .listRowBackground(highlighted ? Color.accentColor.opacity(0.10) : nil)
    }
}

private struct ReportTimelineRow: View {
    let report: ReportEntity
    let highlighted: Bool
    var ongoing = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(report.publicationLabel).font(.headline)
                if ongoing { StatusBadge(text: "継続中", color: .orange) }
                Spacer()
                Text(report.receivedAt, format: .dateTime.hour().minute().second())
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Text(report.title)
                .font(.subheadline.bold())
            Text(report.body)
                .font(.subheadline)
                .textSelection(.enabled)
            if let status = TsunamiPublication(report: report).status {
                Text(status).font(.caption.bold()).foregroundStyle(.blue)
            }
            if let value = report.numericHypocenter {
                DisclosureGroup("数値の詳細") {
                    HypocenterFields(value: value, showMagnitudeAndDepth: report.classification != "eew.warning" && report.eventType != "eew_warning")
                }
                .font(.subheadline)
            }
            if let bulletin = report.bulletin {
                DisclosureGroup("この発表の詳細") { BulletinContent(bulletin: bulletin, reportID: report.id) }
            }
            HStack {
                if report.isCancelled { StatusBadge(text: "取消", color: .orange) }
                if report.isEEW && report.isFinal { StatusBadge(text: "最終", color: .blue) }
                Spacer()
                Text("seq \(report.serverSequence)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 6)
        .listRowBackground(highlighted ? Color.accentColor.opacity(0.12) : nil)
    }
}

private struct HypocenterFields: View {
    let value: HypocenterDTO
    var showMagnitudeAndDepth = true

    var body: some View {
        if let note = value.qualification {
            Label(note, systemImage: "exclamationmark.triangle")
                .font(.subheadline).foregroundStyle(.orange)
        }
        if let epicenter = value.epicenter { LabeledContent(value.label("震源地名"), value: epicenter) }
        if let origin = ServerDateParser.parse(value.originTime) {
            LabeledContent(value.label("地震発生時刻"), value: origin.formatted(date: .abbreviated, time: .standard))
        }
        if let latitude = value.latitude {
            LabeledContent(value.label("緯度"), value: latitude.formatted(.number.precision(.fractionLength(1...4))) + "°")
        }
        if let longitude = value.longitude {
            LabeledContent(value.label("経度"), value: longitude.formatted(.number.precision(.fractionLength(1...4))) + "°")
        }
        if showMagnitudeAndDepth {
            if let magnitude = value.magnitude {
                LabeledContent(value.label("マグニチュード"), value: magnitude.formatted(.number.precision(.fractionLength(1))))
            }
            if let depth = value.depthText { LabeledContent(value.label("深さ"), value: depth) }
        }
    }
}

private struct SourceFile: FileDocument {
    static var readableContentTypes: [UTType] { [.data] }
    var data: Data
    init(data: Data) { self.data = data }
    init(configuration: ReadConfiguration) throws { data = configuration.file.regularFileContents ?? Data() }
    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper { FileWrapper(regularFileWithContents: data) }
}

private struct BulletinContent: View {
    let bulletin: BulletinDTO
    let reportID: String
    var summarizedSections: [BulletinSectionDTO] = []
    @EnvironmentObject private var repository: EventRepository
    @State private var file: SourceFile?
    @State private var exporting = false
    @State private var loading = false
    @State private var error: String?
    @State private var showingIntensityObservations = false

    var body: some View {
        let sections = (bulletin.sections ?? []).enumerated().filter { !summarizedSections.contains($0.element) }
        let observations = sections.filter { $0.element.isIntensityObservation }
        let forecasts = sections.filter { $0.element.isTsunamiForecastArea }
        return VStack(alignment: .leading, spacing: 12) {
            if let headline = bulletin.headline, !headline.isEmpty {
                Text(headline).font(.subheadline).textSelection(.enabled)
            }
            ForEach(sections, id: \.offset) { offset, section in
                if section.isIntensityObservation {
                    if offset == observations.first?.offset {
                        Button {
                            showingIntensityObservations = true
                        } label: {
                            Label("各地の震度を見る（\(observations.count)地域・地点）", systemImage: "list.bullet")
                                .font(.subheadline)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("intensityObservations")
                    }
                } else if !section.isTsunamiForecastArea {
                    BulletinSectionContent(section: section)
                }
            }
            if !forecasts.isEmpty {
                DisclosureGroup("津波予報区の詳細（\(forecasts.count)予報区）") {
                    ForEach(forecasts, id: \.offset) { _, section in
                        BulletinSectionContent(section: section)
                    }
                }
            }
            if let document = bulletin.document {
                if !document.complete {
                    Text("この資料は分割データです。各受信片は発表履歴から保存できます。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Button {
                    loading = true
                    Task {
                        defer { loading = false }
                        do {
                            file = SourceFile(data: try await repository.sourceDocument(reportID: reportID))
                            error = nil
                            exporting = true
                        } catch { self.error = error.localizedDescription }
                    }
                } label: {
                    if loading { ProgressView() } else { Label("原電文を保存", systemImage: "square.and.arrow.down") }
                }
                .disabled(loading)
                if let error { Text(error).font(.caption).foregroundStyle(.red) }
            }
        }
        .fileExporter(isPresented: $exporting, document: file, contentType: .data,
                      defaultFilename: "\(reportID).\(bulletin.document?.fileExtension ?? "bin")") { result in
            if case let .failure(failure) = result { error = failure.localizedDescription }
        }
        .sheet(isPresented: $showingIntensityObservations) {
            IntensityObservationsView(sections: observations.map(\.element))
                .presentationDetents([.medium, .large])
                .presentationDragIndicator(.visible)
        }
    }
}

private struct BulletinSectionContent: View {
    let section: BulletinSectionDTO

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(section.title).font(.subheadline.bold())
            if let text = section.text { Text(text).textSelection(.enabled) }
            ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                LabeledContent(row.label, value: row.value)
            }
        }
    }
}

private extension BulletinSectionDTO {
    var isTsunamiForecastArea: Bool { title.hasPrefix("津波予報区：") }

    var isIntensityObservation: Bool {
        title.hasPrefix("観測") && rows?.contains {
            $0.label == "震度" || $0.label == "最大震度"
        } == true
    }
}

private struct IntensityObservationsView: View {
    let sections: [BulletinSectionDTO]
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ForEach(Array(sections.enumerated()), id: \.offset) { _, section in
                    Section(section.title) {
                        if let text = section.text { Text(text).textSelection(.enabled) }
                        ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                            LabeledContent(row.label, value: row.value)
                        }
                    }
                }
            }
            .navigationTitle("各地の震度")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("閉じる") { dismiss() }
                        .accessibilityIdentifier("closeIntensityObservations")
                }
            }
        }
    }
}
