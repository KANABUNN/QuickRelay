import SwiftUI

// Watch screens. Everything shown comes from the iPhone's WatchSnapshot.
// `detail` is optional: snapshots from older iPhone builds still render with
// the title, status and summary text.

struct WatchHomeView: View {
    @EnvironmentObject private var snapshots: WatchSnapshotStore

    var body: some View {
        NavigationStack {
            TimelineView(.periodic(from: .now, by: 15)) { context in
                let events = pairedEvents
                let tsunami = events.first { $0.activeTsunamiWarning(at: context.date) != nil }
                let ongoingQuakes = events.filter { $0.category == .earthquake && $0.isOngoing(at: context.date) }
                let latestQuake = events.filter { $0.category == .earthquake }.max { $0.updatedAt < $1.updatedAt }
                List {
                    if let tsunami, let warning = tsunami.tsunamiWarning {
                        NavigationLink {
                            WatchEventDetailView(eventID: tsunami.id)
                        } label: {
                            WatchTsunamiWarningView(warning: warning)
                        }
                        .listItemTint(TsunamiKindStyle(kind: warning.title).fill.opacity(0.35))
                    }
                    ForEach(ongoingQuakes) { event in
                        NavigationLink {
                            WatchEventDetailView(eventID: event.id)
                        } label: {
                            WatchEarthquakeRow(event: event, now: context.date, prominent: true)
                        }
                        .listItemTint(event.detail?.isEEWWarning == true ? Color.red.opacity(0.35) : Color.orange.opacity(0.25))
                    }
                    if let latestQuake, !ongoingQuakes.contains(where: { $0.id == latestQuake.id }) {
                        Section("最新の地震") {
                            NavigationLink {
                                WatchEventDetailView(eventID: latestQuake.id)
                            } label: {
                                WatchEarthquakeRow(event: latestQuake, now: context.date, prominent: true)
                            }
                        }
                    }
                    Section {
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
                    }
                    WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                    WatchSyncButton()
                }
                .navigationTitle("Quick Relay")
            }
        }
    }

    private var pairedEvents: [WatchEventSnapshot] {
        snapshots.snapshot?.isPaired == true ? snapshots.snapshot?.events ?? [] : []
    }

    private func categoryLabel(_ title: String, symbol: String, category: WatchEventCategory, now: Date) -> some View {
        let events = pairedEvents.filter { $0.category == category }
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
                    Text("受信した発表に基づく表示です。受信できていない変更がある場合があります。Watchには最近の履歴を表示します。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                WatchSyncButton()
            }
            .navigationTitle(title)
        }
    }

    @ViewBuilder
    private func eventLink(_ event: WatchEventSnapshot, now: Date) -> some View {
        NavigationLink { WatchEventDetailView(eventID: event.id) } label: {
            if event.category == .tsunami {
                WatchTsunamiRow(event: event, now: now)
            } else {
                WatchEarthquakeRow(event: event, now: now)
            }
        }
    }
}

// MARK: - Rows

private struct WatchEarthquakeRow: View {
    let event: WatchEventSnapshot
    let now: Date
    var prominent = false

    var body: some View {
        let detail = event.detail
        HStack(alignment: .top, spacing: 8) {
            IntensityBadge(reading: IntensityReading(detail?.intensity), size: prominent ? 42 : 34,
                           caption: detail?.intensityIsForecast == true ? "予想" : nil)
            VStack(alignment: .leading, spacing: 2) {
                Text(event.title)
                    .font(prominent ? .headline : .subheadline.weight(.semibold))
                    .lineLimit(2)
                Text(timeText)
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
                if let metrics = metricsText {
                    Text(metrics).font(.caption2.monospacedDigit())
                }
                if event.isOngoing(at: now) {
                    WatchOngoingLabel(warning: detail?.isEEWWarning == true)
                } else if detail == nil {
                    Text(event.watchPublicationStatus(at: now)).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var timeText: String {
        DisplayTime.dayTime(event.detail?.originTime ?? event.occurredAt ?? event.updatedAt)
    }

    private var metricsText: String? {
        let parts = [event.detail?.magnitude, event.detail?.depth.map { "深さ" + $0 }].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " ")
    }
}

private struct WatchTsunamiRow: View {
    let event: WatchEventSnapshot
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            if let warning = event.activeTsunamiWarning(at: now) {
                WatchTsunamiWarningView(warning: warning)
            } else {
                Text(event.title).font(.subheadline.weight(.semibold)).lineLimit(2)
                if event.isOngoing(at: now) { WatchOngoingLabel() }
                Text(event.watchPublicationStatus(at: now)).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
            }
            if let observation = event.tsunamiObservations?.max(by: { $0.issuedAt < $1.issuedAt }) {
                WatchTsunamiObservationView(observation: observation, compact: true)
            }
            if event.activeTsunamiWarning(at: now) == nil {
                Text(DisplayTime.dayTime(event.updatedAt))
                    .font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - Detail

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
                    if event.category == .tsunami {
                        WatchTsunamiDetail(event: event, now: context.date)
                    } else {
                        WatchEarthquakeDetail(event: event, now: context.date)
                    }
                    if !event.summary.isEmpty {
                        Section("発表内容") { Text(event.summary).font(.footnote) }
                    }
                    Section("時刻") {
                        if let occurredAt = event.detail?.originTime ?? event.occurredAt {
                            dateRow("発生", date: occurredAt)
                        }
                        dateRow("最新発表", date: event.updatedAt)
                    }
                    WatchFreshnessView(snapshot: snapshots.snapshot, now: context.date)
                    Section {
                        Text("全地域・観測点・発表履歴・電文情報はiPhoneで確認できます。")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                } else {
                    Text("情報が更新されました。履歴一覧から選び直してください。")
                        .font(.callout)
                }
                WatchSyncButton()
            }
            .navigationTitle(event?.category == .tsunami ? "津波" : "地震")
        }
    }

    private func dateRow(_ title: String, date: Date) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption2).foregroundStyle(.secondary)
            Text(DisplayTime.daySeconds(date)).font(.callout.monospacedDigit())
        }
    }
}

private struct WatchEarthquakeDetail: View {
    let event: WatchEventSnapshot
    let now: Date

    var body: some View {
        let detail = event.detail
        Section {
            HStack(alignment: .top, spacing: 8) {
                IntensityBadge(reading: IntensityReading(detail?.intensity), size: 50,
                               caption: detail?.intensityIsForecast == true ? "予想最大" : "最大震度")
                VStack(alignment: .leading, spacing: 2) {
                    Text(event.title).font(.headline).lineLimit(3)
                    if let kind = detail?.kindLabel {
                        Text(kind).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
            }
            if event.isOngoing(at: now) {
                WatchOngoingLabel(warning: detail?.isEEWWarning == true)
                if let reason = event.ongoingReason { Text(reason).font(.caption2) }
            } else {
                Text(event.watchPublicationStatus(at: now)).font(.caption2).foregroundStyle(.secondary)
            }
        }
        if let detail {
            Section("震源") {
                if let magnitude = detail.magnitude { valueRow("規模", magnitude) }
                if let depth = detail.depth { valueRow("深さ", depth) }
                if detail.magnitude == nil && detail.depth == nil {
                    Text(detail.isEEWWarning == true ? "警報のためM・深さは表示しません" : "M・深さの記載なし")
                        .font(.caption2).foregroundStyle(.secondary)
                }
                if let note = detail.hypocenterNote {
                    Text(note).font(.caption2).foregroundStyle(.orange)
                }
            }
            if let comment = detail.comment {
                Section("留意事項") { Text(comment).font(.footnote) }
            }
            if let areas = detail.areas, !areas.isEmpty {
                Section(detail.intensityIsForecast == true ? "予想震度" : "各地の震度") {
                    ForEach(areas) { area in
                        HStack(spacing: 6) {
                            IntensityBadge(reading: IntensityReading(area.intensity), size: 24)
                            Text(area.name).font(.footnote).lineLimit(2)
                        }
                    }
                }
            }
        }
    }

    private func valueRow(_ title: String, _ value: String) -> some View {
        HStack {
            Text(title).font(.caption2).foregroundStyle(.secondary)
            Spacer()
            Text(value).font(.body.monospacedDigit().weight(.semibold))
        }
    }
}

private struct WatchTsunamiDetail: View {
    let event: WatchEventSnapshot
    let now: Date

    var body: some View {
        Section {
            if let warning = event.activeTsunamiWarning(at: now) {
                WatchTsunamiWarningView(warning: warning)
            } else {
                Text(event.title).font(.headline)
                if event.isOngoing(at: now) {
                    WatchOngoingLabel()
                }
            }
            if let reason = event.ongoingReason, event.isOngoing(at: now) {
                Text(reason).font(.caption2)
            }
            Text(event.watchPublicationStatus(at: now)).font(.caption2).foregroundStyle(.secondary)
        }
        ForEach(event.detail?.tsunamiLevels ?? []) { level in
            Section {
                ForEach(level.areas) { area in
                    HStack(alignment: .firstTextBaseline) {
                        Text(area.name).font(.footnote).lineLimit(2)
                        Spacer(minLength: 4)
                        if let height = area.height {
                            Text(height).font(.footnote.weight(.semibold)).multilineTextAlignment(.trailing)
                        }
                    }
                }
                if let remaining = level.remaining, remaining > 0 {
                    Text("ほか\(remaining)予報区（iPhoneで確認）").font(.caption2).foregroundStyle(.secondary)
                }
            } header: {
                let style = TsunamiKindStyle(kind: level.title)
                Text(level.title)
                    .font(.caption.weight(.bold))
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .foregroundStyle(style.foreground)
                    .background(style.fill, in: RoundedRectangle(cornerRadius: 4))
            }
        }
        ForEach(event.tsunamiObservations ?? []) { observation in
            Section {
                WatchTsunamiObservationView(observation: observation)
            }
        }
    }
}

// MARK: - Shared pieces

private struct WatchTsunamiWarningView: View {
    let warning: WatchTsunamiWarning

    var body: some View {
        let style = TsunamiKindStyle(kind: warning.title)
        VStack(alignment: .leading, spacing: 3) {
            Text(warning.title)
                .font(.headline)
                .padding(.horizontal, 6).padding(.vertical, 2)
                .foregroundStyle(style.foreground)
                .background(style.fill, in: RoundedRectangle(cornerRadius: 5))
            Text(warning.scope).font(.caption)
            Text("発表 " + DisplayTime.dayTime(warning.issuedAt))
                .font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
        }
    }
}

private struct WatchTsunamiObservationView: View {
    let observation: WatchTsunamiObservation
    var compact = false

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(observation.source).font(.caption2).foregroundStyle(.secondary)
            Text(observation.name).font(.caption.bold()).lineLimit(compact ? 2 : nil)
            if let maximum = observation.maximumWave {
                Text("最大波 \(maximum)").font(.caption).lineLimit(compact ? 3 : nil)
                    .foregroundStyle(maximum.contains("上昇中") ? Color.red : Color.primary)
            }
            if let first = observation.firstWave {
                Text("第1波 \(first)").font(.caption).lineLimit(compact ? 3 : nil)
            }
            if !compact {
                Text("発表 " + DisplayTime.dayTime(observation.issuedAt))
                    .font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
            }
        }
    }
}

private struct WatchOngoingLabel: View {
    var warning = false

    var body: some View {
        Label(warning ? "警報 継続中" : "継続中", systemImage: warning ? "exclamationmark.triangle.fill" : "dot.radiowaves.left.and.right")
            .font(.caption.bold())
            .foregroundStyle(warning ? Color.red : Color.orange)
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
                        Text(DisplayTime.dayTime(syncedAt) + "（" + DisplayTime.age(syncedAt, now: now) + "）")
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
                    Text("Watch用情報の作成 " + DisplayTime.dayTime(snapshot.generatedAt))
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
    func activeTsunamiWarning(at date: Date) -> WatchTsunamiWarning? {
        isOngoing(at: date) ? tsunamiWarning : nil
    }

    func watchPublicationStatus(at date: Date) -> String {
        if isOngoing, ongoingUntil != nil, !isOngoing(at: date) {
            return category == .tsunami ? "津波予報の有効期間を終了" : "緊急地震速報の対象時間を終了"
        }
        return publicationStatus
    }
}
