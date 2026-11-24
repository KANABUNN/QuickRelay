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
        var detailEventID: String? = nil

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
        Self.eventURL(eventID)
    }

    func detailURL(for state: ContentState) -> URL? {
        state.detailEventID.flatMap(Self.eventURL) ?? detailURL
    }

    private static func eventURL(_ eventID: String) -> URL? {
        guard !eventID.isEmpty,
              eventID.unicodeScalars.allSatisfy({ CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-").contains($0) }) else { return nil }
        return URL(string: "quake-relay://event/\(eventID)")
    }
}

extension QuickRelayActivityAttributes.ContentState {
    /// Intensity parsed from `intensityText` ("最大震度 5弱"); nil when absent or unknown.
    var intensityReading: IntensityReading? {
        guard category == "earthquake" else { return nil }
        return IntensityReading(intensityText)
    }

    var isInactive: Bool { ended || cancelled }
}

/// Leading indicator: JMA-coloured intensity for earthquakes, a wave mark for tsunami.
struct QuickRelayActivityIndicator: View {
    let state: QuickRelayActivityAttributes.ContentState
    let isStale: Bool
    var size: CGFloat = 56

    var body: some View {
        Group {
            if state.category == "earthquake" {
                IntensityBadge(reading: state.intensityReading, size: size, caption: "予想最大")
            } else {
                Image(systemName: state.symbol)
                    .font(.system(size: size * 0.42, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: size, height: size)
                    .background(state.warning && !state.isInactive ? Color.orange : Color.blue,
                                in: RoundedRectangle(cornerRadius: size * 0.22, style: .continuous))
            }
        }
        .opacity(state.isInactive || isStale ? 0.55 : 1)
        .accessibilityHidden(state.category != "earthquake")
    }
}

// Shared with the widget so layout tests measure the actual Lock Screen card.
struct QuickRelayLiveActivityCard: View {
    let state: QuickRelayActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            QuickRelayActivityIndicator(state: state, isStale: isStale)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(state.displayTitle)
                        .font(.headline)
                        .lineLimit(1)
                        .minimumScaleFactor(0.75)
                    Spacer(minLength: 4)
                    Text(state.reportLabel)
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                        .lineLimit(1)
                }
                Text(state.summary)
                    .font(.caption)
                    .lineLimit(2)
                Text(state.freshnessLabel(isStale: isStale))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                    .foregroundStyle(isStale && !state.ended ? .orange : .secondary)
                HStack(spacing: 4) {
                    Text("発表")
                    Text(Date(timeIntervalSince1970: TimeInterval(state.reportedAt)), style: .time)
                    Spacer()
                    Label("Quick Relay", systemImage: state.symbol)
                        .labelStyle(.titleAndIcon)
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }
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

/// Intensity text in a JMA-coloured capsule for the compact Dynamic Island.
struct QuickRelayCompactIntensity: View {
    let state: QuickRelayActivityAttributes.ContentState

    var body: some View {
        if let reading = state.intensityReading, let level = reading.dominant {
            Text(reading.mainText + (reading.openEnded ? "+" : ""))
                .font(.caption2.weight(.heavy))
                .monospacedDigit()
                .lineLimit(1)
                .padding(.horizontal, 4)
                .foregroundStyle(level.foreground)
                .background(level.fill, in: Capsule())
        } else {
            Image(systemName: state.symbol)
        }
    }
}
