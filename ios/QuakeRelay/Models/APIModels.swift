import Foundation

enum RelayEventType: String, Codable, CaseIterable, Identifiable, Sendable {
    case earthquakeInfo = "earthquake_info"
    case eewForecast = "eew_forecast"
    case eewWarning = "eew_warning"
    case eewCancel = "eew_cancel"
    case earthquakeUpdate = "earthquake_update"
    case systemTest = "system_test"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .earthquakeInfo: "地震情報"
        case .eewForecast: "緊急地震速報（予報）"
        case .eewWarning: "緊急地震速報（警報）"
        case .eewCancel: "緊急地震速報（取消）"
        case .earthquakeUpdate: "地震情報（更新）"
        case .systemTest: "システムテスト"
        }
    }

    var isEEW: Bool {
        self == .eewForecast || self == .eewWarning || self == .eewCancel
    }

    var defaultInterruptionLevel: String {
        isEEW ? "time-sensitive" : "active"
    }
}

struct HypocenterDTO: Codable, Equatable, Sendable {
    var status: String
    var note: String? = nil
    var originTime: String? = nil
    var epicenter: String? = nil
    var latitude: Double? = nil
    var longitude: Double? = nil
    var depthKm: Int? = nil
    var depthCondition: String? = nil
    var magnitude: Double? = nil

    var isAssumed: Bool { status == "assumed" }
    var qualification: String? {
        if let note, !note.isEmpty { return note }
        if isAssumed { return "仮定震源の参考値です。実際の震源要素を示す値ではありません。" }
        if status == "low_accuracy" { return "精度の低い推定です。続報で変わる場合があります。" }
        return nil
    }
    func label(_ name: String) -> String { isAssumed ? "\(name)（仮定値）" : name }
    var depthText: String? {
        guard let depthKm else { return nil }
        if depthCondition == "ごく浅い" { return "ごく浅い（数値 \(depthKm) km）" }
        if depthCondition == "７００ｋｍ以上" { return "\(depthKm) km以上" }
        return "\(depthKm) km"
    }
}

struct EventDTO: Codable, Equatable, Sendable {
    let id: String
    let category: String
    let eventType: String
    let originTime: String?
    let epicenter: String?
    let latitude: Double?
    let longitude: Double?
    let depthKm: Int?
    let magnitude: Double?
    let maxIntensity: String?
    let latestRevision: Int?
    let isFinal: Bool
    let isCancelled: Bool
    let latestReportAt: String?
    // latestRevision is the server state version, not a DMDATA report number.
    var sourceSerial: Int? = nil
    var classification: String? = nil
    var telegramType: String? = nil
    var isWarning: Bool? = nil
    var hypocenter: HypocenterDTO? = nil
}

struct ReportDTO: Codable, Equatable, Sendable {
    let id: String
    let eventId: String
    let serverSequence: UInt64
    let messageId: String
    let eventType: String
    let revision: Int?
    let isFinal: Bool
    let isCancelled: Bool
    let title: String
    let body: String
    let occurredAt: String?
    let receivedAt: String
    let createdAt: String?
    var classification: String? = nil
    var telegramType: String? = nil
    var hypocenter: HypocenterDTO? = nil
    var originTime: String? = nil
    var epicenter: String? = nil
    var latitude: Double? = nil
    var longitude: Double? = nil
    var depthKm: Int? = nil
    var magnitude: Double? = nil
    var maxIntensity: String? = nil
    var isWarning: Bool? = nil

    var numericHypocenter: HypocenterDTO? {
        if let hypocenter { return hypocenter }
        guard originTime != nil || epicenter != nil || latitude != nil || longitude != nil ||
                depthKm != nil || magnitude != nil else { return nil }
        return HypocenterDTO(status: "estimated", originTime: originTime, epicenter: epicenter,
                             latitude: latitude, longitude: longitude, depthKm: depthKm, magnitude: magnitude)
    }
}

struct SyncItemDTO: Codable, Equatable, Sendable {
    let report: ReportDTO
    let event: EventDTO
}

struct SyncResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let items: [SyncItemDTO]
    let nextAfterSequence: UInt64
    let hasMore: Bool
    let latestCommittedSequence: UInt64
    let serverTime: String
}

struct EventsResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let items: [EventDTO]
    let nextCursor: String?
    let serverTime: String
}

struct EventDetailResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let event: EventDTO
    let reports: [ReportDTO]
}

struct SuccessResponse: Decodable { let ok: Bool }

struct PairCompleteRequest: Encodable, Sendable {
    let pairingCode: String
    let installationId: String
}

struct PairCompleteResponse: Decodable, Equatable, Sendable {
    let ok: Bool
    let deviceAccessToken: String
    let expiresAt: String?
}

struct DevicePreferences: Codable, Equatable, Sendable {
    var notificationsEnabled: Bool
    var timeSensitiveEnabled: Bool
    var customSoundEnabled: Bool
    var eventTypes: [String]
}

struct DeviceRegistrationRequest: Encodable, Sendable {
    let installationId: String
    let deviceToken: String
    let environment: String
    let appVersion: String
    let osVersion: String
    let deviceName: String
    let preferences: DevicePreferences?
}

struct DeviceDTO: Codable, Equatable, Sendable {
    let installationId: String
    let deviceName: String?
    let environment: String
    let appVersion: String?
    let osVersion: String?
    let active: Bool
    let preferences: DevicePreferences
    let lastSeenAt: String?
}

struct DeviceResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let device: DeviceDTO
}

struct PreferencesPatchRequest: Encodable, Sendable {
    let notificationsEnabled: Bool?
    let timeSensitiveEnabled: Bool?
    let customSoundEnabled: Bool?
    let eventTypes: [String]?
}

struct PreferencesResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let preferences: DevicePreferences
}

struct HealthResponse: Codable, Equatable, Sendable {
    let ok: Bool
    let version: String
    let db: Bool
    let apnsHttp2: Bool
    let configLoaded: Bool
    let migrationCurrent: Bool
    let time: String
}

struct APIErrorEnvelope: Decodable, Sendable {
    struct Details: Decodable, Sendable {
        let code: String
        let message: String
    }

    let ok: Bool
    let error: Details
    let requestId: String?
}

enum ServerDateParser {
    private static let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let standard: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    static func parse(_ value: String?) -> Date? {
        guard let value else { return nil }
        return fractional.date(from: value) ?? standard.date(from: value)
    }

    static func string(from date: Date) -> String {
        fractional.string(from: date)
    }
}

extension JSONDecoder {
    static var quakeRelay: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }
}

extension JSONEncoder {
    static var quakeRelay: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return encoder
    }
}
