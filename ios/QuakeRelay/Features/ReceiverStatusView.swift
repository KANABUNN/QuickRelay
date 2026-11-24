import SwiftUI

enum ReceiverState: Equatable {
    case healthy, disconnected, unchecked, unavailable, offline
    var label: String {
        switch self {
        case .healthy: "受信接続を確認"
        case .disconnected: "受信接続が途切れています"
        case .unchecked: "現在の受信状態は未確認"
        case .unavailable: "サーバーに接続できません"
        case .offline: "情報の受信を停止中"
        }
    }
    static func resolve(status: ReceiverStatusResponse?, checkedAt: Date?, failed: Bool, now: Date) -> ReceiverState {
        if failed { return .unavailable }
        guard let status, let checkedAt, now.timeIntervalSince(checkedAt) >= -5,
              now.timeIntervalSince(checkedAt) < 75 else { return .unchecked }
        guard status.sourceConfigured != nil, status.sourceFresh != nil else { return .unchecked }
        if status.sourceConfigured == false { return .offline }
        if status.db == false || !status.ok { return .unavailable }
        return status.sourceFresh == true ? .healthy : .disconnected
    }
}

struct ReceiverStatusView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var repository: EventRepository

    var body: some View {
        TimelineView(.periodic(from: .now, by: 15)) { context in
            let state = ReceiverState.resolve(status: environment.receiverStatus,
                checkedAt: environment.receiverStatusCheckedAt,
                failed: environment.receiverStatusError != nil, now: context.date)
            let timing = timingText
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Label(state.label, systemImage: state == .healthy ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(state == .healthy ? Color.green : Color.orange)
                    .lineLimit(1)
                    .accessibilityIdentifier("receiverStatus")
                Spacer(minLength: 4)
                Text(timing)
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
            }
            .padding(.horizontal).padding(.vertical, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.thinMaterial, ignoresSafeAreaEdges: [])
        }
    }

    private var timingText: String {
        var checked = "未実行"
        if let date = environment.receiverStatusCheckedAt { checked = DisplayTime.time(date) }
        var synced = "未実行"
        if let date = repository.lastSyncAt { synced = DisplayTime.time(date) }
        return "確認 \(checked) · 同期 \(synced)"
    }
}
