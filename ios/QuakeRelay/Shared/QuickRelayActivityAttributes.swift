import ActivityKit
import Foundation

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

        func freshnessLabel(isStale: Bool) -> String {
            if ended { return statusText }
            return isStale ? "更新が途切れています・最新情報を確認" : statusText
        }
        var symbol: String {
            if cancelled || ended { return "info.circle" }
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
