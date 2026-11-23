import Foundation
import SwiftData
import SwiftUI

/// Detail of one event (earthquake, tsunami or related information).
/// The category decides the summary layout; the change list, the complete
/// report history and the telegram metadata are shared by all categories.
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
                    content(event: event, now: context.date)
                } else {
                    ContentUnavailableView(
                        "情報を読み込めません",
                        systemImage: "exclamationmark.icloud",
                        description: Text("同期後も見つからない場合は、対象の情報がサーバーに残っているか確認してください。")
                    )
                }
            }
        }
        .navigationTitle(navigationTitle)
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
                        .disabled(mapReport == nil).accessibilityIdentifier("showEventMap")
                }
            }
        }
        .sheet(isPresented: $showingMap) {
            if let mapReport { EventMapView(report: mapReport).presentationDetents([.large]) }
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

    private func content(event: EventEntity, now: Date) -> some View {
        let continuation = EventContinuationIndex(reports: allReports).status(for: event, now: now)
        return ScrollViewReader { proxy in
            List {
                switch event.category {
                case "tsunami":
                    TsunamiDetailSections(event: event, reports: reports, continuation: continuation,
                                          highlightedReportID: highlightedReportID)
                case "earthquake":
                    EarthquakeDetailSections(event: event, reports: reports, continuation: continuation)
                default:
                    AdvisoryDetailSections(event: event, report: latestReport ?? reports.last)
                }
                if let latestReport {
                    ReportChangesView(report: latestReport, previous: ReportComparison.previous(to: latestReport, in: reports))
                }
                ReportTimelineSection(reports: sortedReports, continuation: continuation,
                                      highlightedReportID: highlightedReportID)
                if let latestReport {
                    ReportMetadataSection(report: latestReport, showsSourceButton: event.category != "advisory")
                }
            }
            .listStyle(.insetGrouped)
            .onAppear { scrollToHighlightedReport(using: proxy) }
            .onChange(of: reports.count) { _, _ in
                scrollToHighlightedReport(using: proxy)
            }
        }
    }

    private var navigationTitle: String {
        guard let event = events.first else { return "情報の詳細" }
        switch event.category {
        case "tsunami":
            return TsunamiPublication.latest(in: reports, eventID: eventID)?.title ?? event.displayTitle
        case "earthquake":
            return EarthquakeEventSummary(reports: reports).hypocenterReport?.numericHypocenter?.epicenter ?? event.displayTitle
        default:
            return event.displayTitle
        }
    }

    private var sortedReports: [ReportEntity] {
        ReportTimelineDisplay.visible(reports).sorted(by: ReportTimelineOrder.areInAscendingOrder)
    }

    private var latestReport: ReportEntity? {
        guard let version = events.first?.latestRevision else { return nil }
        return reports.first { $0.serverSequence == Int64(version) }
    }

    /// Earthquakes map the most informative non-cancelled report; tsunami the latest one.
    private var mapReport: ReportEntity? {
        guard let event = events.first else { return latestReport }
        if event.category == "earthquake" {
            let summary = EarthquakeEventSummary(reports: reports)
            return summary.observedReport ?? summary.hypocenterReport ?? latestReport
        }
        return latestReport
    }

    private func markReadIfVisible() {
        guard isVisible, scenePhase == .active, let event = events.first else { return }
        history.markRead(event, scope: scope)
    }

    private func scrollToHighlightedReport(using proxy: ScrollViewProxy) {
        guard let highlightedReportID else { return }
        if let report = TsunamiPublication.latest(in: reports, eventID: eventID), report.id == highlightedReportID,
           TsunamiPublication(report: report).status != nil {
            return
        }
        if TsunamiObservation.groups(in: reports).contains(where: { $0.report.id == highlightedReportID }) { return }
        DispatchQueue.main.async {
            withAnimation { proxy.scrollTo(highlightedReportID, anchor: .center) }
        }
    }
}
