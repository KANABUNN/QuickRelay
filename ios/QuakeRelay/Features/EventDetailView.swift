import Foundation
import SwiftData
import SwiftUI
import UniformTypeIdentifiers

struct EventDetailView: View {
    let eventID: String
    let highlightedReportID: String?

    @EnvironmentObject private var repository: EventRepository
    @Query private var events: [EventEntity]
    @Query private var reports: [ReportEntity]

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
        Group {
            if let event = events.first {
                ScrollViewReader { proxy in
                    List {
                        latestSection(event)
                        if event.isWarningProduct && !event.isCancelled { forecastReferenceSection }
                        if let latestReport, let bulletin = latestReport.bulletin {
                            Section("発表内容") { BulletinContent(bulletin: bulletin, reportID: latestReport.id) }
                        }
                        timelineSection
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
        .navigationTitle(events.first?.displayTitle ?? "情報の詳細")
        .navigationBarTitleDisplayMode(.inline)
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
    private func latestSection(_ event: EventEntity) -> some View {
        Section("最新状態") {
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
                Label("この情報は取り消されました", systemImage: "xmark.octagon.fill")
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

    private var timelineSection: some View {
        Section("発表履歴") {
            if reports.isEmpty {
                Text("発表履歴はまだ同期されていません。")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(sortedReports) { report in
                    ReportTimelineRow(
                        report: report,
                        highlighted: report.id == highlightedReportID
                    )
                    .id(report.id)
                }
            }
        }
    }

    private var sortedReports: [ReportEntity] {
        ReportTimelineDisplay.visible(reports).sorted(by: ReportTimelineOrder.areInAscendingOrder)
    }

    private var latestReport: ReportEntity? {
        guard let version = events.first?.latestRevision else { return nil }
        return reports.first { $0.serverSequence == Int64(version) }
    }

    private func scrollToHighlightedReport(using proxy: ScrollViewProxy) {
        guard let highlightedReportID else { return }
        DispatchQueue.main.async {
            withAnimation { proxy.scrollTo(highlightedReportID, anchor: .center) }
        }
    }
}

private struct ReportTimelineRow: View {
    let report: ReportEntity
    let highlighted: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(report.publicationLabel).font(.headline)
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
    @EnvironmentObject private var repository: EventRepository
    @State private var file: SourceFile?
    @State private var exporting = false
    @State private var loading = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let headline = bulletin.headline, !headline.isEmpty {
                Text(headline).font(.subheadline).textSelection(.enabled)
            }
            ForEach(Array((bulletin.sections ?? []).enumerated()), id: \.offset) { _, section in
                VStack(alignment: .leading, spacing: 6) {
                    Text(section.title).font(.subheadline.bold())
                    if let text = section.text { Text(text).textSelection(.enabled) }
                    ForEach(Array((section.rows ?? []).enumerated()), id: \.offset) { _, row in
                        LabeledContent(row.label, value: row.value)
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
    }
}
