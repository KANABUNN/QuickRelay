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

    func isOngoing(at date: Date) -> Bool {
        isOngoing && (ongoingUntil.map { date < $0 } ?? true)
    }
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
