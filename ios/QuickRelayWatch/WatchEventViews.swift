import SwiftUI

struct WatchHomeView: View {
    @EnvironmentObject private var snapshots: WatchSnapshotStore

    var body: some View {
        NavigationStack {
            TimelineView(.periodic(from: .now, by: 15)) { context in
                List {
                    WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                    NavigationLink {
                        WatchEventListView(category: .earthquake)
                    } label: {
                        categoryLabel("地震", symbol: "waveform.path.ecg", category: .earthquake, now: context.date)
                    }
                    NavigationLink {
                        WatchEventListView(category: .tsunami)
                    } label: {
                        categoryLabel("津波", symbol: "water.waves", category: .tsunami, now: context.date)
                    }
                    WatchSyncButton()
                }
                .navigationTitle("Quick Relay")
            }
        }
    }

    private func categoryLabel(_ title: String, symbol: String, category: WatchEventCategory, now: Date) -> some View {
        let events = snapshots.snapshot?.isPaired == true ? snapshots.snapshot?.events.filter { $0.category == category } ?? [] : []
        let ongoing = events.filter { $0.isOngoing(at: now) }.count
        return VStack(alignment: .leading, spacing: 3) {
            Label(title, systemImage: symbol).font(.headline)
            Text(ongoing > 0 ? "継続中 \(ongoing)件・履歴 \(events.count)件" : "履歴 \(events.count)件")
                .font(.caption)
                .foregroundStyle(ongoing > 0 ? Color.orange : Color.secondary)
        }
        .padding(.vertical, 3)
    }
}

private struct WatchEventListView: View {
    let category: WatchEventCategory
    @EnvironmentObject private var snapshots: WatchSnapshotStore
    private var title: String { category == .tsunami ? "津波" : "地震" }
    private var events: [WatchEventSnapshot] {
        guard snapshots.snapshot?.isPaired == true else { return [] }
        return (snapshots.snapshot?.events ?? []).filter { $0.category == category }.sorted {
            if $0.updatedAt != $1.updatedAt { return $0.updatedAt > $1.updatedAt }
            return $0.id < $1.id
        }
    }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            let ongoing = events.filter { $0.isOngoing(at: context.date) }
            List {
                WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                if !ongoing.isEmpty {
                    Section("発表中") {
                        ForEach(ongoing) { event in
                            eventLink(event, now: context.date).id("ongoing-" + event.id)
                        }
                    }
                }
                Section("履歴") {
                    if events.isEmpty {
                        Text("\(title)の受信履歴はありません")
                            .font(.callout).foregroundStyle(.secondary)
                    } else {
                        ForEach(events) { event in
                            eventLink(event, now: context.date).id("history-" + event.id)
                        }
                    }
                }
                Section {
                    Text("受信した発表に基づく表示です。受信できていない変更がある場合があります。")
                        .font(.footnote).foregroundStyle(.secondary)
                    Text("Watchには最近の履歴を表示します。すべての履歴はiPhoneで確認できます。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                WatchSyncButton()
            }
            .navigationTitle(title)
        }
    }

    private func eventLink(_ event: WatchEventSnapshot, now: Date) -> some View {
        NavigationLink { WatchEventDetailView(eventID: event.id) } label: {
            WatchEventRow(event: event, now: now)
        }
    }
}

private struct WatchEventRow: View {
    let event: WatchEventSnapshot
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(event.title).font(.headline).lineLimit(3)
            if event.isOngoing(at: now) {
                WatchOngoingLabel()
            }
            Text(event.watchPublicationStatus(at: now)).font(.caption).foregroundStyle(.secondary).lineLimit(3)
            if !event.summary.isEmpty { Text(event.summary).font(.caption).lineLimit(3) }
            Text(event.updatedAt, format: .dateTime.month().day().hour().minute())
                .font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
        }
        .padding(.vertical, 3)
    }
}

private struct WatchEventDetailView: View {
    let eventID: String
    @EnvironmentObject private var snapshots: WatchSnapshotStore
    private var event: WatchEventSnapshot? {
        guard snapshots.snapshot?.isPaired == true else { return nil }
        return snapshots.snapshot?.events.first { $0.id == eventID }
    }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            List {
                if let event {
                    Section {
                        Text(event.title).font(.headline)
                        if event.isOngoing(at: context.date) {
                            WatchOngoingLabel()
                            if let reason = event.ongoingReason { Text(reason).font(.caption) }
                        }
                        Text(event.watchPublicationStatus(at: context.date)).font(.callout)
                    }
                    if !event.summary.isEmpty {
                        Section("発表内容") { Text(event.summary).font(.callout) }
                    }
                    Section("時刻") {
                        if let occurredAt = event.occurredAt {
                            dateRow("発生", date: occurredAt)
                        }
                        dateRow("最新発表", date: event.updatedAt)
                    }
                    WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                    Section {
                        Text("地域別の内容と発表履歴はiPhoneで確認してください。")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                } else {
                    Text("情報が更新されました。履歴一覧から選び直してください。")
                        .font(.callout)
                }
                WatchSyncButton()
            }
            .navigationTitle("詳細")
        }
    }

    private func dateRow(_ title: String, date: Date) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(date, format: .dateTime.month().day().hour().minute().second())
                .font(.callout.monospacedDigit())
        }
    }
}

private struct WatchOngoingLabel: View {
    var body: some View {
        Label("継続中", systemImage: "dot.radiowaves.left.and.right")
            .font(.caption.bold()).foregroundStyle(.orange)
    }
}

private struct WatchFreshnessView: View {
    let snapshot: WatchSnapshot?
    let now: Date

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 4) {
                if let snapshot, snapshot.isPaired {
                    if let syncedAt = snapshot.lastSyncAt {
                        Text("iPhoneのサーバー同期")
                            .font(.caption2).foregroundStyle(.secondary)
                        Text(syncedAt, format: .dateTime.month().day().hour().minute())
                            .font(.caption.monospacedDigit())
                        if now.timeIntervalSince(syncedAt) > 300 {
                            Label("最新状態は未確認", systemImage: "clock.badge.exclamationmark")
                                .font(.caption.bold()).foregroundStyle(.orange)
                        }
                    } else {
                        Text("サーバー同期は未確認")
                            .font(.caption.bold()).foregroundStyle(.orange)
                    }
                    if let receiverSummary = snapshot.receiverSummary {
                        Text(receiverSummary).font(.caption2).foregroundStyle(.secondary)
                    }
                    Text("Watch用情報の作成 \(snapshot.generatedAt.formatted(.dateTime.month().day().hour().minute()))")
                        .font(.caption2).foregroundStyle(.secondary)
                } else {
                    Text(snapshot == nil ? "iPhoneから情報を受信してください" : "iPhoneのサーバー接続が解除されています")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
    }
}

private struct WatchSyncButton: View {
    @EnvironmentObject private var snapshots: WatchSnapshotStore

    var body: some View {
        Section {
            Button {
                snapshots.requestLatest()
            } label: {
                if snapshots.isRequesting {
                    HStack { ProgressView(); Text("同期中") }
                } else {
                    Label("iPhoneと同期", systemImage: "arrow.clockwise")
                }
            }
            .disabled(snapshots.isRequesting)
            if let message = snapshots.message {
                Text(message).font(.footnote).foregroundStyle(.secondary)
            }
        }
    }
}

private extension WatchEventSnapshot {
    func watchPublicationStatus(at date: Date) -> String {
        if isOngoing, ongoingUntil != nil, !isOngoing(at: date) {
            return category == .tsunami ? "津波予報の有効期間を終了" : "緊急地震速報の対象時間を終了"
        }
        return publicationStatus
    }
}
