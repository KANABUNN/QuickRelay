import Foundation
import SwiftData

@Model
final class EventEntity {
    @Attribute(.unique) var id: String
    var category: String
    var eventType: String
    var originTime: Date?
    var epicenter: String?
    var latitude: Double?
    var longitude: Double?
    var depthKm: Int?
    var magnitude: Double?
    var maxIntensity: String?
    var latestRevision: Int?
    var sourceSerial: Int? = nil
    var classification: String? = nil
    var hypocenterData: Data? = nil
    var isFinal: Bool
    var isCancelled: Bool
    var latestReportAt: Date

    init(dto: EventDTO) {
        id = dto.id
        category = dto.category
        eventType = dto.eventType
        originTime = ServerDateParser.parse(dto.originTime)
        epicenter = dto.epicenter
        latitude = dto.latitude
        longitude = dto.longitude
        depthKm = dto.depthKm
        magnitude = dto.magnitude
        maxIntensity = dto.maxIntensity
        latestRevision = dto.latestRevision
        sourceSerial = dto.sourceSerial
        classification = dto.classification
        hypocenterData = dto.hypocenter.flatMap { try? JSONEncoder.quakeRelay.encode($0) }
        isFinal = dto.isFinal
        isCancelled = dto.isCancelled
        latestReportAt = ServerDateParser.parse(dto.latestReportAt) ?? .distantPast
    }

    func merge(_ dto: EventDTO) {
        // The VPS has already resolved ordering separately for each telegram type.
        // Applying sticky terminal flags here would cancel later, independent products.
        guard EventMergePolicy.shouldReplaceCurrentState(existing: self, incoming: dto) else { return }
        category = dto.category
        eventType = dto.eventType
        originTime = ServerDateParser.parse(dto.originTime)
        epicenter = dto.epicenter
        latitude = dto.latitude
        longitude = dto.longitude
        depthKm = dto.depthKm
        magnitude = dto.magnitude
        maxIntensity = dto.maxIntensity
        latestRevision = dto.latestRevision
        sourceSerial = dto.sourceSerial
        classification = dto.classification
        hypocenterData = dto.hypocenter.flatMap { try? JSONEncoder.quakeRelay.encode($0) }
        latestReportAt = ServerDateParser.parse(dto.latestReportAt) ?? latestReportAt
        isFinal = dto.isFinal
        isCancelled = dto.isCancelled
    }

    var numericHypocenter: HypocenterDTO {
        if let hypocenterData, let value = try? JSONDecoder.quakeRelay.decode(HypocenterDTO.self, from: hypocenterData) {
            return value
        }
        return HypocenterDTO(status: "estimated", originTime: originTime.map { ServerDateParser.string(from: $0) },
                             epicenter: epicenter, latitude: latitude, longitude: longitude,
                             depthKm: depthKm, magnitude: magnitude)
    }

    var intensityLabel: String {
        RelayEventType(rawValue: eventType)?.isEEW == true ? "予想最大震度" : "観測最大震度"
    }
    var isWarningProduct: Bool { classification == "eew.warning" || eventType == "eew_warning" }
}

@Model
final class ReportEntity {
    @Attribute(.unique) var id: String
    var eventId: String
    var serverSequence: Int64
    var messageId: String
    var eventType: String
    var revision: Int?
    var isFinal: Bool
    var isCancelled: Bool
    var title: String
    var body: String
    var occurredAt: Date?
    var receivedAt: Date
    var createdAt: Date?
    var classification: String? = nil
    var telegramType: String? = nil
    var hypocenterData: Data? = nil
    var maxIntensity: String? = nil
    var isWarning: Bool? = nil

    init(dto: ReportDTO, serverSequence: Int64) {
        id = dto.id
        eventId = dto.eventId
        self.serverSequence = serverSequence
        messageId = dto.messageId
        eventType = dto.eventType
        revision = dto.revision
        isFinal = dto.isFinal
        isCancelled = dto.isCancelled
        title = dto.title
        body = dto.body
        occurredAt = ServerDateParser.parse(dto.occurredAt)
        receivedAt = ServerDateParser.parse(dto.receivedAt) ?? .distantPast
        createdAt = ServerDateParser.parse(dto.createdAt)
        classification = dto.classification
        telegramType = dto.telegramType
        hypocenterData = dto.numericHypocenter.flatMap { try? JSONEncoder.quakeRelay.encode($0) }
        maxIntensity = dto.maxIntensity
        isWarning = dto.isWarning
    }

    func update(from dto: ReportDTO, serverSequence: Int64) {
        eventId = dto.eventId
        self.serverSequence = serverSequence
        messageId = dto.messageId
        eventType = dto.eventType
        revision = dto.revision
        isFinal = dto.isFinal
        isCancelled = dto.isCancelled
        title = dto.title
        body = dto.body
        occurredAt = ServerDateParser.parse(dto.occurredAt)
        receivedAt = ServerDateParser.parse(dto.receivedAt) ?? receivedAt
        createdAt = ServerDateParser.parse(dto.createdAt)
        classification = dto.classification
        telegramType = dto.telegramType
        hypocenterData = dto.numericHypocenter.flatMap { try? JSONEncoder.quakeRelay.encode($0) }
        maxIntensity = dto.maxIntensity
        isWarning = dto.isWarning
    }

    var numericHypocenter: HypocenterDTO? {
        guard let hypocenterData else { return nil }
        return try? JSONDecoder.quakeRelay.decode(HypocenterDTO.self, from: hypocenterData)
    }
}

@Model
final class SyncCursorEntity {
    @Attribute(.unique) var id: String
    var lastServerSequence: Int64
    var latestCommittedSequence: Int64
    var lastSyncAt: Date?
    var serverIdentity: String? = nil

    init(
        id: String = SyncCursorEntity.singletonID,
        lastServerSequence: Int64 = 0,
        latestCommittedSequence: Int64 = 0,
        lastSyncAt: Date? = nil
    ) {
        self.id = id
        self.lastServerSequence = lastServerSequence
        self.latestCommittedSequence = latestCommittedSequence
        self.lastSyncAt = lastSyncAt
    }

    static let singletonID = "main"
}

enum EventMergePolicy {
    static func shouldReplaceCurrentState(existing: EventEntity, incoming: EventDTO) -> Bool {
        switch (existing.latestRevision, incoming.latestRevision) {
        case let (existingRevision?, incomingRevision?):
            return incomingRevision > existingRevision
        case (nil, .some):
            return true
        case (.some, nil):
            return false
        case (nil, nil):
            break
        }

        let incomingDate = ServerDateParser.parse(incoming.latestReportAt) ?? .distantPast
        return incomingDate > existing.latestReportAt
    }
}

enum ReportTimelineOrder {
    static func areInAscendingOrder(_ lhs: ReportEntity, _ rhs: ReportEntity) -> Bool {
        let lhsTime = lhs.occurredAt ?? lhs.receivedAt
        let rhsTime = rhs.occurredAt ?? rhs.receivedAt
        if lhsTime != rhsTime { return lhsTime < rhsTime }
        return lhs.serverSequence < rhs.serverSequence
    }
}

enum ForecastReference {
    // Resolve only the VXSE45 stream. Receipt order alone can select a delayed,
    // older report; cancellation and final reports must remain terminal.
    static func latest(eventID: String, reports: [ReportEntity]) -> ReportEntity? {
        let forecasts = reports.filter {
            $0.eventId == eventID && ($0.telegramType == "VXSE45" ||
                $0.classification == "eew.forecast" ||
                ($0.telegramType == nil && $0.classification == nil && $0.eventType == "eew_forecast"))
        }.sorted { $0.serverSequence < $1.serverSequence }
        var current: ReportEntity?
        for report in forecasts {
            if let old = current {
                if replaces(report, old) { current = report }
            } else { current = report }
        }
        guard let current, !current.isCancelled else { return nil }
        // Old app caches did not retain the cancellation's source product.
        if reports.contains(where: {
            $0.eventId == eventID && $0.isCancelled && $0.telegramType == nil &&
                $0.classification == nil && $0.serverSequence > current.serverSequence
        }) { return nil }
        return current
    }

    private static func replaces(_ report: ReportEntity, _ old: ReportEntity) -> Bool {
        if let revision = report.revision, let previous = old.revision, revision != previous {
            return revision > previous && !(old.isCancelled || old.isFinal && !report.isCancelled)
        }
        if old.isCancelled { return false }
        if report.isCancelled { return true }
        if old.isFinal && !report.isFinal { return false }
        if report.isFinal && !old.isFinal { return true }
        if report.isWarning == true && old.isWarning != true { return true }
        return (report.occurredAt ?? report.receivedAt) > (old.occurredAt ?? old.receivedAt)
    }
}

enum PersistenceSchema {
    static let models: [any PersistentModel.Type] = [
        EventEntity.self,
        ReportEntity.self,
        SyncCursorEntity.self
    ]
}
