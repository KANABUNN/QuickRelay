import SwiftData
import SwiftUI

/// First tab: receiver health, everything currently announced, recent
/// earthquakes with simple counts, and the latest tsunami/related information.
struct OverviewView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var repository: EventRepository
    @EnvironmentObject private var history: HistoryPreferences
    @EnvironmentObject private var router: DeepLinkRouter
    @Query(sort: \EventEntity.latestReportAt, order: .reverse) private var events: [EventEntity]
    @Query private var reports: [ReportEntity]
    private var scope: String { repository.serverIdentity.isEmpty ? environment.settings.serverBaseURL : repository.serverIdentity }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            overview(now: context.date)
        }
        .navigationTitle("概況")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                if repository.isSyncing {
                    ProgressView()
                } else {
                    Button("同期", systemImage: "arrow.clockwise") {
                        Task {
                            _ = await repository.syncAll()
                            await environment.refreshReceiverStatus()
                        }
                    }
                }
            }
        }
    }

    private func overview(now: Date) -> some View {
        let index = EventContinuationIndex(reports: reports)
        let groups = Dictionary(grouping: reports, by: \.eventId)
        let ongoing = events.filter { $0.category != "advisory" && index.status(for: $0, now: now).isOngoing }
        let earthquakes = events.filter { $0.category == "earthquake" }
        let latestTsunami = events.first { $0.category == "tsunami" }
        let latestAdvisory = events.first { $0.category == "advisory" }
        return List {
            Section {
                ReceiverStatusDetail(now: now)
            } header: {
                Text("受信状態")
            }
            Section {
                if ongoing.isEmpty {
                    Label("発表中の緊急地震速報・津波警報等はありません", systemImage: "checkmark.circle")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                ForEach(ongoing) { event in
                    link(event, groups: groups, continuation: index.status(for: event, now: now))
                }
            } header: {
                Text("発表中")
            } footer: {
                Text("受信した最新の発表に基づきます。受信が途切れていた間の変更は反映されません。")
            }
            Section {
                EarthquakeStatistics(earthquakes: earthquakes, now: now)
            } header: {
                Text("地震の受信件数")
            }
            Section {
                if earthquakes.isEmpty {
                    Text("地震の受信履歴はありません").foregroundStyle(.secondary)
                }
                ForEach(Array(earthquakes.prefix(5))) { event in
                    link(event, groups: groups, continuation: index.status(for: event, now: now))
                }
                Button("地震の履歴をすべて見る") { router.selectedTab = .events }
            } header: {
                Text("最近の地震")
            }
            Section {
                if let latestTsunami {
                    link(latestTsunami, groups: groups, continuation: index.status(for: latestTsunami, now: now))
                } else {
                    Text("津波の受信履歴はありません").foregroundStyle(.secondary)
                }
                Button("津波の履歴を見る") { router.selectedTab = .tsunami }
            } header: {
                Text("津波（最新）")
            }
            Section {
                if let latestAdvisory {
                    link(latestAdvisory, groups: groups, continuation: .history)
                } else {
                    Text("関連情報の受信履歴はありません").foregroundStyle(.secondary)
                }
                Button("関連情報の履歴を見る") { router.selectedTab = .advisory }
            } header: {
                Text("南海トラフ・関連情報（最新）")
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            _ = await repository.syncAll()
            await environment.refreshReceiverStatus()
        }
    }

    private func link(_ event: EventEntity, groups: [String: [ReportEntity]], continuation: EventContinuation) -> some View {
        NavigationLink(value: AppRoute.event(id: event.id, reportID: nil)) {
            EventRow(event: event, reports: groups[event.id] ?? [], unread: history.isUnread(event, scope: scope),
                     pinned: history.isPinned(event.id, scope: scope), continuation: continuation)
        }
    }
}

/// Receiver health with the raw counters reported by the VPS.
private struct ReceiverStatusDetail: View {
    let now: Date
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var repository: EventRepository

    var body: some View {
        let state = ReceiverState.resolve(status: environment.receiverStatus,
            checkedAt: environment.receiverStatusCheckedAt,
            failed: environment.receiverStatusError != nil, now: now)
        let status = environment.receiverStatus
        Label(state.label, systemImage: state == .healthy ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
            .font(.headline)
            .foregroundStyle(state == .healthy ? Color.green : Color.orange)
        if let error = environment.receiverStatusError {
            Text(error).font(.caption).foregroundStyle(.orange)
        }
        Group {
            KeyValueRow(label: "接続の確認", value: timeAndAge(environment.receiverStatusCheckedAt), monospaced: true)
            if let status {
                KeyValueRow(label: "DMDATA接続", value: status.source.connected ? "接続中" : "切断",
                            valueColor: status.source.connected ? nil : Color.orange)
                KeyValueRow(label: "最終フレーム受信", value: timeAndAge(ServerDateParser.parse(status.source.lastFrameAt)), monospaced: true)
                KeyValueRow(label: "最終電文受信", value: timeAndAge(ServerDateParser.parse(status.source.lastDataAt)), monospaced: true)
                KeyValueRow(label: "再接続・拒否", value: "再接続 \(status.source.reconnects)回 · 拒否 \(status.source.rejected)件", monospaced: true)
                KeyValueRow(label: "DB・APNs", value: (status.db == false ? "DB異常" : "DB正常") + " · " + (status.apnsConfigured ? "APNs設定済み" : "APNs未設定"))
            }
            KeyValueRow(label: "履歴の同期", value: timeAndAge(repository.lastSyncAt), monospaced: true)
            KeyValueRow(label: "server_sequence",
                        value: "\(repository.lastServerSequence) / 確定 \(repository.latestCommittedSequence)", monospaced: true)
        }
    }

    private func timeAndAge(_ date: Date?) -> String {
        guard let date else { return "未確認" }
        return DisplayTime.daySeconds(date) + "（" + DisplayTime.age(date, now: now) + "）"
    }
}

/// Received earthquake counts and the strongest observed intensity.
private struct EarthquakeStatistics: View {
    let earthquakes: [EventEntity]
    let now: Date

    var body: some View {
        HStack(spacing: 10) {
            tile("24時間", seconds: 86_400)
            tile("7日間", seconds: 86_400 * 7)
            tile("30日間", seconds: 86_400 * 30)
        }
        Text("最大震度は観測値（震度速報・震源・震度情報）だけを集計します。EEWのみの地震は件数に含め、最大震度には含めません。")
            .font(.caption2)
            .foregroundStyle(.secondary)
    }

    private func tile(_ title: String, seconds: TimeInterval) -> some View {
        let since = now.addingTimeInterval(-seconds)
        let matching = earthquakes.filter { (ValueText.originTime($0.numericHypocenter) ?? $0.latestReportAt) >= since }
        let strongest = matching.filter { !$0.isEEW && !$0.isCancelled }
            .compactMap { IntensityReading($0.maxIntensity) }
            .max { IntensityReading.sortKey($0) < IntensityReading.sortKey($1) }
        return VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            HStack(spacing: 8) {
                IntensityBadge(reading: strongest, size: 32)
                VStack(alignment: .leading, spacing: 0) {
                    Text("\(matching.count)").font(.title3.weight(.semibold)).monospacedDigit()
                    Text("件").font(.caption2).foregroundStyle(.secondary)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
