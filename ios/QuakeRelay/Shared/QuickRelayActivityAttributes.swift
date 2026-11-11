import ActivityKit
import Foundation
import SwiftUI

// Both the application and WidgetKit extension compile this type. ActivityKit
// uses the default Codable keys, not the REST API's snake_case strategy.
struct QuickRelayActivityAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable, Sendable {
        var title: String
        var summary: String
        var statusText: String
        var intensityText: String
        var reportLabel: String
        var category: String
        var warning: Bool
        var ended: Bool
        var cancelled: Bool
        var reportedAt: Int64
        var updatedAt: Int64
        var eewWarning: Bool? = nil

        var hasActiveEEWWarning: Bool {
            category == "earthquake" && eewWarning == true && !ended && !cancelled
        }

        func highlightsEEWWarning(isStale: Bool) -> Bool {
            hasActiveEEWWarning && !isStale
        }

        var displayTitle: String {
            hasActiveEEWWarning ? "緊急地震速報（警報）" : title
        }

        func freshnessLabel(isStale: Bool) -> String {
            if ended { return statusText }
            return isStale ? "更新が途切れています・最新情報を確認" : statusText
        }
        var symbol: String {
            if cancelled || ended { return "info.circle" }
            if hasActiveEEWWarning { return "exclamationmark.triangle.fill" }
            return category == "tsunami" ? "water.waves" : "waveform.path.ecg"
        }
    }
    var eventID: String
    var telegramType: String
    var startSequence: Int64 = 0

    var detailURL: URL? {
        guard !eventID.isEmpty,
              eventID.unicodeScalars.allSatisfy({ CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-").contains($0) }) else { return nil }
        return URL(string: "quake-relay://event/\(eventID)")
    }
}

// Shared with the widget so layout tests measure the actual Lock Screen card.
struct QuickRelayLiveActivityCard: View {
    let state: QuickRelayActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Label("Quick Relay", systemImage: state.symbol).font(.caption.weight(.semibold))
                Spacer()
                Text(state.reportLabel).font(.caption).lineLimit(1)
            }
            Text(state.displayTitle).font(.headline).lineLimit(1).minimumScaleFactor(0.85)
            if !state.intensityText.isEmpty {
                Text(state.intensityText).font(.headline).lineLimit(1)
            }
            Text(state.summary).font(.caption).lineLimit(1)
            Text(state.freshnessLabel(isStale: isStale))
                .font(.caption.weight(.semibold)).lineLimit(2)
                .foregroundStyle(isStale && !state.ended ? .orange : .secondary)
            HStack {
                Text("発表")
                Text(Date(timeIntervalSince1970: TimeInterval(state.reportedAt)), style: .time)
                Spacer()
                Text("タップして詳細")
            }.font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .padding(14)
        .overlay {
            if state.highlightsEEWWarning(isStale: isStale) {
                RoundedRectangle(cornerRadius: 26, style: .continuous)
                    .strokeBorder(.red, lineWidth: 3)
                    .padding(3)
                    .accessibilityHidden(true)
                    .allowsHitTesting(false)
            }
        }
        // The system can truncate Live Activities beyond 160 points. Keep the
        // compact card readable, with full text available through its detail URL.
        .dynamicTypeSize(...DynamicTypeSize.large)
    }
}
