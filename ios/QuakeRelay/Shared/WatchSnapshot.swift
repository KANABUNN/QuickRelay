import Foundation

enum WatchEventCategory: String, Codable, CaseIterable, Sendable {
    case earthquake, tsunami
    var title: String { self == .earthquake ? "地震" : "津波" }
}

struct WatchEventSnapshot: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var category: WatchEventCategory
    var title: String
    var summary: String
    var occurredAt: Date?
    var updatedAt: Date
    var publicationStatus: String
    var isOngoing: Bool
    var ongoingReason: String?
    var ongoingUntil: Date? = nil
    var tsunamiWarning: WatchTsunamiWarning? = nil
    var tsunamiObservations: [WatchTsunamiObservation]? = nil
    /// Structured display values. Optional so older iPhone and Watch builds
    /// keep decoding the same schema version.
    var detail: WatchEventDetail? = nil

    func isOngoing(at date: Date) -> Bool {
        isOngoing && (ongoingUntil.map { date < $0 } ?? true)
    }
}

struct WatchEventDetail: Codable, Equatable, Sendable {
    /// e.g. "震源・震度情報 · 発表" or "緊急地震速報（予報） 第3報"
    var kindLabel: String? = nil
    /// Observed (or, when `intensityIsForecast`, forecast) maximum intensity text.
    var intensity: String? = nil
    var intensityIsForecast: Bool? = nil
    var originTime: Date? = nil
    var magnitude: String? = nil
    var depth: String? = nil
    var hypocenterNote: String? = nil
    var isEEWWarning: Bool? = nil
    /// 防災上の留意事項, e.g. the tsunami comment of an earthquake bulletin.
    var comment: String? = nil
    var areas: [WatchAreaIntensity]? = nil
    var tsunamiLevels: [WatchTsunamiLevel]? = nil
}

struct WatchAreaIntensity: Codable, Equatable, Identifiable, Sendable {
    var name: String
    var intensity: String
    var id: String { name }
}

struct WatchTsunamiLevel: Codable, Equatable, Identifiable, Sendable {
    var title: String
    var areas: [WatchTsunamiArea]
    var remaining: Int? = nil
    var id: String { title }
}

struct WatchTsunamiArea: Codable, Equatable, Identifiable, Sendable {
    var name: String
    var height: String? = nil
    var id: String { name }
}

struct WatchTsunamiWarning: Codable, Equatable, Sendable {
    var title: String
    var scope: String
    var issuedAt: Date
}

struct WatchTsunamiObservation: Codable, Equatable, Identifiable, Sendable {
    var id: String { source }
    var source: String
    var name: String
    var issuedAt: Date
    var firstWave: String?
    var maximumWave: String?
}

// Only display data crosses WatchConnectivity; credentials stay on the iPhone.
// Both targets use the same default Codable date representation.
struct WatchSnapshot: Codable, Equatable, Sendable {
    static let currentSchemaVersion = 1
    static let applicationContextKey = "quickRelaySnapshot"
    static let requestMessageKey = "quickRelayRequestSnapshot"

    var schemaVersion: Int = currentSchemaVersion
    var generatedAt: Date
    var lastSyncAt: Date?
    var receiverSummary: String?
    var events: [WatchEventSnapshot]
    var isPaired: Bool = true

    func encoded() throws -> Data { try JSONEncoder().encode(self) }

    static func decode(_ data: Data) throws -> WatchSnapshot {
        let value = try JSONDecoder().decode(Self.self, from: data)
        guard value.schemaVersion == currentSchemaVersion else {
            throw DecodingError.dataCorrupted(.init(codingPath: [], debugDescription: "Unsupported Watch snapshot version"))
        }
        return value
    }
}
