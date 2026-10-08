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
            VStack(alignment: .leading, spacing: 4) {
                Label(state.label, systemImage: state == .healthy ? "checkmark.circle" : "exclamationmark.circle")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(state == .healthy ? Color.green : Color.orange)
                    .accessibilityIdentifier("receiverStatus")
                HStack {
                    Text("接続の確認 \(environment.receiverStatusCheckedAt?.formatted(date: .omitted, time: .standard) ?? "未実行")")
                    Spacer()
                    Text("履歴の同期 \(repository.lastSyncAt?.formatted(date: .omitted, time: .shortened) ?? "未実行")")
                }
                .font(.caption).foregroundStyle(.secondary)
            }
            .padding(.horizontal).padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.thinMaterial)
        }
    }
}
