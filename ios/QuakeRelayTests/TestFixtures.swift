import Foundation
@testable import QuakeRelay

enum TestFixtures {
    static func event(
        id: String = "evt_001",
        eventType: String = "eew_warning",
        magnitude: Double? = 6.2,
        maxIntensity: String? = "5強",
        latestRevision: Int? = 4,
        isFinal: Bool = false,
        isCancelled: Bool = false,
        latestReportAt: String? = "2026-08-13T13:30:05.000Z"
    ) -> EventDTO {
        EventDTO(
            id: id,
            category: "earthquake",
            eventType: eventType,
            originTime: "2026-08-13T13:29:58.000Z",
            epicenter: "日向灘",
            latitude: 31.8,
            longitude: 131.7,
            depthKm: 30,
            magnitude: magnitude,
            maxIntensity: maxIntensity,
            latestRevision: latestRevision,
            isFinal: isFinal,
            isCancelled: isCancelled,
            latestReportAt: latestReportAt
        )
    }

    static func report(
        id: String = "rpt_001",
        eventID: String = "evt_001",
        sequence: UInt64 = 1,
        messageID: String = "msg_001",
        revision: Int? = 4
    ) -> ReportDTO {
        ReportDTO(
            id: id,
            eventId: eventID,
            serverSequence: sequence,
            messageId: messageID,
            eventType: "eew_warning",
            revision: revision,
            isFinal: false,
            isCancelled: false,
            title: "緊急地震速報（警報） 第4報",
            body: "震源：日向灘\n最大震度：5強\nM6.2",
            occurredAt: "2026-08-13T13:30:01.123Z",
            receivedAt: "2026-08-13T13:30:01.456Z",
            createdAt: "2026-08-13T13:30:02.000Z"
        )
    }

    static func page(
        items: [SyncItemDTO],
        next: UInt64,
        hasMore: Bool = false,
        latest: UInt64? = nil
    ) -> SyncResponse {
        SyncResponse(
            ok: true,
            items: items,
            nextAfterSequence: next,
            hasMore: hasMore,
            latestCommittedSequence: latest ?? next,
            serverTime: "2026-08-13T13:31:00.000Z"
        )
    }
}
