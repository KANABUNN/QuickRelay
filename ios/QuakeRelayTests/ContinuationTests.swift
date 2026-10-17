import Foundation
import SwiftData
import XCTest
@testable import QuakeRelay

@MainActor
final class ContinuationTests: XCTestCase {
    private let now = ServerDateParser.parse("2026-10-17T03:00:30Z")!

    private func event(_ id: String = "event", category: String = "earthquake",
                       type: String = "earthquake_info") -> EventEntity {
        EventEntity(dto: EventDTO(id: id, category: category, eventType: type,
            originTime: nil, epicenter: nil, latitude: nil, longitude: nil,
            depthKm: nil, magnitude: nil, maxIntensity: nil, latestRevision: 99,
            isFinal: false, isCancelled: false, latestReportAt: "2026-10-17T03:00:20Z"))
    }

    private func report(_ id: String, eventID: String = "event", sequence: Int64,
                        type: String, category: String,
                        revision: Int? = nil, final: Bool = false, cancelled: Bool = false,
                        occurredAt: String? = "2026-10-17T03:00:00Z",
                        receivedAt: String = "2026-10-17T03:00:01Z",
                        validUntil: String? = nil,
                        sections: [BulletinSectionDTO] = []) -> ReportEntity {
        let isEEW = ["VXSE43", "VXSE44", "VXSE45"].contains(type)
        let eventType = isEEW
            ? (cancelled ? "eew_cancel" : type == "VXSE43" ? "eew_warning" : "eew_forecast")
            : category == "tsunami" ? "tsunami_info" : "earthquake_info"
        var dto = ReportDTO(id: id, eventId: eventID, serverSequence: UInt64(sequence),
            messageId: "message-" + id, eventType: eventType, revision: revision,
            isFinal: final || (isEEW && cancelled), isCancelled: cancelled,
            title: "合成テスト", body: "実際の防災情報ではありません。",
            occurredAt: occurredAt, receivedAt: receivedAt, createdAt: nil)
        dto.category = category
        dto.classification = isEEW ? (type == "VXSE43" ? "eew.warning" : "eew.forecast") : "telegram.earthquake"
        dto.telegramType = type
        dto.infoType = cancelled ? "取消" : "発表"
        dto.isWarning = type == "VXSE43"
        dto.validUntil = validUntil
        dto.bulletin = BulletinDTO(sections: sections)
        return ReportEntity(dto: dto, serverSequence: sequence)
    }

    private func area(_ name: String, kind: String, previous: String? = nil) -> BulletinSectionDTO {
        var rows = [BulletinRowDTO(label: "発表", value: kind)]
        if let previous { rows.append(BulletinRowDTO(label: "前回", value: previous)) }
        return BulletinSectionDTO(title: "津波予報区：" + name, rows: rows)
    }

    func testLatestNationalTsunamiReleaseRetiresOldEventWhenEarthquakeIDSetChanges() {
        let warning = report("warning", eventID: "tsunami-old-earthquake", sequence: 1,
            type: "VTSE41", category: "tsunami", sections: [area("合成沿岸", kind: "津波警報")])
        let release = report("release", eventID: "tsunami-combined-earthquakes", sequence: 2,
            type: "VTSE41", category: "tsunami", occurredAt: "2026-10-17T03:00:10Z",
            sections: [area("合成沿岸", kind: "津波警報解除", previous: "津波警報")])
        // Receiving the older publication later must not reactivate its old ID.
        let lateOriginal = report("late-original", eventID: warning.eventId, sequence: 3,
            type: "VTSE41", category: "tsunami", receivedAt: "2026-10-17T03:00:29Z",
            sections: [area("合成沿岸", kind: "津波警報")])
        let index = EventContinuationIndex(reports: [lateOriginal, release, warning])
        XCTAssertEqual(index.latestTsunami?.id, release.id)
        XCTAssertEqual(index.status(for: event(warning.eventId, category: "tsunami"), now: now).state, .history)
        let current = index.status(for: event(release.eventId, category: "tsunami"), now: now)
        XCTAssertEqual(current.state, .ended)
        XCTAssertFalse(current.isOngoing)
        XCTAssertTrue(current.reportIDs.isEmpty)
    }

    func testPartialReleaseKeepsRemainingTsunamiWarningOngoing() {
        let partial = report("partial", sequence: 1, type: "VTSE41", category: "tsunami",
            sections: [area("解除沿岸", kind: "津波注意報解除", previous: "津波注意報"),
                       area("継続沿岸", kind: "津波警報")])
        let status = EventContinuationIndex(reports: [partial])
            .status(for: event(category: "tsunami"), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([partial.id]))
        XCTAssertNil(status.until)
    }

    func testCode53MajorTsunamiWarningNameWithColonIsOngoing() {
        let major = report("major", sequence: 1, type: "VTSE41", category: "tsunami",
            sections: [area("合成沿岸", kind: "大津波警報：発表")])
        XCTAssertEqual(TsunamiPublication(report: major).areas.first?.warning, true)
        XCTAssertEqual(EventContinuationIndex(reports: [major])
            .status(for: event(category: "tsunami"), now: now).state, .ongoing)
    }

    func testLaterTsunamiObservationsDoNotReplacePublicationState() {
        let warning = report("warning", sequence: 1, type: "VTSE41", category: "tsunami",
            sections: [area("合成沿岸", kind: "津波注意報")])
        let observed = report("observed", sequence: 2, type: "VTSE51", category: "tsunami",
            occurredAt: "2026-10-17T03:00:10Z")
        let offshore = report("offshore", sequence: 3, type: "VTSE52", category: "tsunami",
            occurredAt: "2026-10-17T03:00:20Z")
        let index = EventContinuationIndex(reports: [offshore, observed, warning])
        XCTAssertEqual(index.latestTsunami?.id, warning.id)
        let status = index.status(for: event(category: "tsunami", type: "tsunami_info"), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([warning.id]))
    }

    func testForecastContinuesAfterWarningReleaseUntilExplicitExpiry() throws {
        let forecast = report("forecast", sequence: 1, type: "VTSE41", category: "tsunami",
            validUntil: "2026-10-17T12:01:00+09:00",
            sections: [area("合成沿岸", kind: "津波予報（若干の海面変動）", previous: "津波警報")])
        let expiry = try XCTUnwrap(ServerDateParser.parse("2026-10-17T03:01:00Z"))
        XCTAssertEqual(forecast.validUntil, expiry)
        let index = EventContinuationIndex(reports: [forecast])
        let current = event(category: "tsunami")
        let status = index.status(for: current, now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.until, expiry)
        XCTAssertEqual(status.reportIDs, Set([forecast.id]))
        XCTAssertTrue(status.explanation.contains("解除"))
        XCTAssertEqual(index.status(for: current, now: expiry).state, .ended)
        XCTAssertEqual(index.status(for: current, now: expiry.addingTimeInterval(1)).state, .ended)
    }

    func testForecastWithUnknownExpiryDoesNotRevivePreviousWarning() {
        let warning = report("warning", sequence: 1, type: "VTSE41", category: "tsunami",
            sections: [area("合成沿岸", kind: "津波警報")])
        let forecast = report("forecast", sequence: 2, type: "VTSE41", category: "tsunami",
            occurredAt: "2026-10-17T03:00:10Z",
            sections: [area("合成沿岸", kind: "津波予報（若干の海面変動）", previous: "津波警報")])
        let status = EventContinuationIndex(reports: [warning, forecast])
            .status(for: event(category: "tsunami"), now: now)
        XCTAssertEqual(status.state, .unknown)
        XCTAssertFalse(status.isOngoing)
        XCTAssertNil(status.until)
        XCTAssertTrue(status.reportIDs.isEmpty)
    }

    func testTsunamiCancellationIsUnknownAndLateWarningDoesNotReactivateIt() {
        let warning = report("warning", sequence: 1, type: "VTSE41", category: "tsunami",
            sections: [area("合成沿岸", kind: "津波警報")])
        let cancel = report("cancel", sequence: 2, type: "VTSE41", category: "tsunami",
            cancelled: true, occurredAt: "2026-10-17T03:00:10Z")
        let lateWarning = report("late", sequence: 3, type: "VTSE41", category: "tsunami",
            receivedAt: "2026-10-17T03:00:29Z", sections: [area("合成沿岸", kind: "津波警報")])
        let index = EventContinuationIndex(reports: [warning, cancel, lateWarning])
        XCTAssertEqual(index.latestTsunami?.id, cancel.id)
        let status = index.status(for: event(category: "tsunami"), now: now)
        XCTAssertEqual(status.state, .unknown)
        XCTAssertFalse(status.isOngoing)
        XCTAssertTrue(status.explanation.contains("解除を意味しません"))
        XCTAssertTrue(status.reportIDs.isEmpty)
    }

    func testNewPublicationAfterTsunamiCancellationCanEstablishOngoingState() {
        let cancel = report("cancel", sequence: 1, type: "VTSE41", category: "tsunami", cancelled: true)
        let later = report("new-publication", sequence: 2, type: "VTSE41", category: "tsunami",
            occurredAt: "2026-10-17T03:00:20Z", sections: [area("合成沿岸", kind: "津波注意報")])
        let status = EventContinuationIndex(reports: [cancel, later])
            .status(for: event(category: "tsunami"), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([later.id]))
    }

    func testOrdinaryEarthquakeInformationRemainsHistory() {
        let ordinary = report("ordinary", sequence: 1, type: "VXSE53", category: "earthquake")
        let index = EventContinuationIndex(reports: [ordinary])
        XCTAssertEqual(index.status(for: event(), now: now).state, .history)
        XCTAssertEqual(index.status(for: event("no-reports"), now: now).state, .history)
    }

    func testFinalModernForecastEndsFreshWarningForSameEarthquakeOnly() {
        let final = report("final", sequence: 1, type: "VXSE45", category: "earthquake",
            revision: 3, final: true)
        let warning = report("warning", sequence: 2, type: "VXSE43", category: "earthquake",
            revision: 1, occurredAt: "2026-10-17T03:00:20Z")
        let other = report("other-warning", eventID: "other-event", sequence: 3,
            type: "VXSE43", category: "earthquake", revision: 1, occurredAt: "2026-10-17T03:00:20Z")
        let index = EventContinuationIndex(reports: [warning, other, final])
        XCTAssertEqual(index.status(for: event(), now: now).state, .ended)
        let otherStatus = index.status(for: event("other-event"), now: now)
        XCTAssertEqual(otherStatus.state, .ongoing)
        XCTAssertEqual(otherStatus.reportIDs, Set([other.id]))
    }

    func testForecastCancellationDoesNotEndFreshWarningStream() {
        let cancel = report("forecast-cancel", sequence: 1, type: "VXSE45", category: "earthquake",
            revision: 3, cancelled: true)
        let warning = report("warning", sequence: 2, type: "VXSE43", category: "earthquake",
            revision: 1, occurredAt: "2026-10-17T03:00:20Z")
        let status = EventContinuationIndex(reports: [cancel, warning])
            .status(for: event(), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([warning.id]))
    }

    func testWarningCancellationDoesNotEndForecastStream() {
        let forecast = report("forecast", sequence: 1, type: "VXSE45", category: "earthquake", revision: 2)
        let cancel = report("warning-cancel", sequence: 2, type: "VXSE43", category: "earthquake",
            revision: 99, cancelled: true, occurredAt: "2026-10-17T03:00:20Z")
        let status = EventContinuationIndex(reports: [forecast, cancel])
            .status(for: event(), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([forecast.id]))
    }

    func testTerminalEEWStreamCannotReviveWithHigherSerial() {
        for cancelled in [false, true] {
            let terminal = report("terminal", sequence: 1, type: "VXSE45", category: "earthquake",
                revision: 2, final: !cancelled, cancelled: cancelled)
            let laterNonfinal = report("late-nonfinal", sequence: 2, type: "VXSE45", category: "earthquake",
                revision: 3, occurredAt: "2026-10-17T03:00:20Z")
            let status = EventContinuationIndex(reports: [laterNonfinal, terminal])
                .status(for: event(), now: now)
            XCTAssertEqual(status.state, .ended, "cancelled=\(cancelled)")
            XCTAssertTrue(status.reportIDs.isEmpty)
        }
    }

    func testHistoricalEEWDoesNotBecomeOngoingWhenReceivedRecently() {
        let historical = report("historical", sequence: 1, type: "VXSE45", category: "earthquake",
            revision: 1, occurredAt: "2026-10-17T02:58:00Z", receivedAt: "2026-10-17T03:00:29Z")
        let status = EventContinuationIndex(reports: [historical])
            .status(for: event(), now: now)
        XCTAssertEqual(status.state, .ended)
        XCTAssertFalse(status.isOngoing)
    }

    func testModernForecastSuppressesFreshLegacyStreamEvenAfterModernCancellation() {
        let modernCancel = report("modern-cancel", sequence: 1, type: "VXSE45", category: "earthquake",
            revision: 1, cancelled: true)
        let legacy = report("legacy", sequence: 2, type: "VXSE44", category: "earthquake",
            revision: 99, occurredAt: "2026-10-17T03:00:20Z")
        XCTAssertEqual(EventContinuationIndex(reports: [modernCancel, legacy])
            .status(for: event(), now: now).state, .ended)
        XCTAssertEqual(EventContinuationIndex(reports: [legacy])
            .status(for: event(), now: now).state, .ongoing)
    }

    func testEEWRevisionsAreComparedWithinIndependentTelegramStreams() throws {
        let forecast = report("forecast-r2", sequence: 1, type: "VXSE45", category: "earthquake", revision: 2)
        let warning = report("warning-r1", sequence: 2, type: "VXSE43", category: "earthquake",
            revision: 1, occurredAt: "2026-10-17T03:00:10Z")
        let olderForecast = report("forecast-r1-late", sequence: 3, type: "VXSE45", category: "earthquake",
            revision: 1, occurredAt: "2026-10-17T03:00:20Z")
        let status = EventContinuationIndex(reports: [olderForecast, warning, forecast])
            .status(for: event(), now: now)
        XCTAssertEqual(status.state, .ongoing)
        XCTAssertEqual(status.reportIDs, Set([forecast.id, warning.id]))
        XCTAssertEqual(status.until, try XCTUnwrap(ServerDateParser.parse("2026-10-17T03:01:10Z")))
    }

    func testEEWExplicitExpiryCanShortenDisplayDuration() throws {
        let forecast = report("short", sequence: 1, type: "VXSE45", category: "earthquake",
            revision: 1, validUntil: "2026-10-17T03:00:40Z")
        let expiry = try XCTUnwrap(forecast.validUntil)
        let index = EventContinuationIndex(reports: [forecast])
        XCTAssertEqual(index.status(for: event(), now: now).until, expiry)
        XCTAssertEqual(index.status(for: event(), now: expiry).state, .ended)
    }

    func testReportValidityDecodesAsOptionalAndUpdatesStoredEntity() throws {
        var dto = ReportDTO(id: "validity", eventId: "event", serverSequence: 1,
            messageId: "message", eventType: "tsunami_info", revision: nil,
            isFinal: false, isCancelled: false, title: "合成テスト", body: "合成データ",
            occurredAt: "2026-10-17T03:00:00Z", receivedAt: "2026-10-17T03:00:01Z", createdAt: nil)
        dto.category = "tsunami"
        dto.telegramType = "VTSE41"
        let oldWire = try JSONEncoder.quakeRelay.encode(dto)
        let oldDTO = try JSONDecoder.quakeRelay.decode(ReportDTO.self, from: oldWire)
        XCTAssertNil(oldDTO.validUntil)
        let stored = ReportEntity(dto: oldDTO, serverSequence: 1)
        XCTAssertNil(stored.validUntil)
        dto.validUntil = "2026-10-17T12:01:00+09:00"
        let received = try JSONDecoder.quakeRelay.decode(ReportDTO.self, from: JSONEncoder.quakeRelay.encode(dto))
        stored.update(from: received, serverSequence: 2)
        XCTAssertEqual(stored.validUntil, ServerDateParser.parse("2026-10-17T03:01:00Z"))
        stored.update(from: oldDTO, serverSequence: 3)
        XCTAssertNil(stored.validUntil)
    }

    func testWatchSnapshotRoundTripRetainsExplicitExpiryAndOngoingState() throws {
        let expiry = now.addingTimeInterval(30)
        let active = WatchEventSnapshot(id: "tsunami", category: .tsunami,
            title: "合成津波情報", summary: "合成沿岸に注意報", occurredAt: now.addingTimeInterval(-30),
            updatedAt: now, publicationStatus: "発表", isOngoing: true,
            ongoingReason: "津波注意報が継続中", ongoingUntil: expiry)
        let history = WatchEventSnapshot(id: "earthquake", category: .earthquake,
            title: "合成地震情報", summary: "観測震度3", occurredAt: now.addingTimeInterval(-60),
            updatedAt: now, publicationStatus: "発表", isOngoing: false, ongoingReason: nil)
        let source = WatchSnapshot(generatedAt: now, lastSyncAt: now.addingTimeInterval(-1),
            receiverSummary: "受信済み", events: [active, history])
        let decoded = try WatchSnapshot.decode(source.encoded())
        XCTAssertEqual(decoded, source)
        XCTAssertTrue(decoded.events[0].isOngoing(at: now))
        XCTAssertFalse(decoded.events[0].isOngoing(at: expiry))
        XCTAssertFalse(decoded.events[0].isOngoing(at: expiry.addingTimeInterval(1)))
        XCTAssertFalse(decoded.events[1].isOngoing(at: now))
    }

    func testWatchSnapshotRejectsUnsupportedSchema() throws {
        var source = WatchSnapshot(generatedAt: now, events: [])
        source.schemaVersion = WatchSnapshot.currentSchemaVersion + 1
        let data = try source.encoded()
        XCTAssertThrowsError(try WatchSnapshot.decode(data)) { error in
            guard case DecodingError.dataCorrupted = error else {
                return XCTFail("Unexpected error for unsupported schema: \(error)")
            }
        }
    }

    func testWatchEncodedKeysContainOnlyDisplayData() throws {
        let display = WatchEventSnapshot(id: "event", category: .tsunami, title: "合成テスト",
            summary: "津波予報", occurredAt: now, updatedAt: now, publicationStatus: "発表",
            isOngoing: true, ongoingReason: "有効期間内", ongoingUntil: now.addingTimeInterval(60))
        let source = WatchSnapshot(generatedAt: now, lastSyncAt: now,
            receiverSummary: "受信済み", events: [display])
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: source.encoded()) as? [String: Any])
        XCTAssertEqual(Set(object.keys), Set(["schemaVersion", "generatedAt", "lastSyncAt",
                                             "receiverSummary", "events", "isPaired"]))
        let events = try XCTUnwrap(object["events"] as? [[String: Any]])
        let keys = Set(try XCTUnwrap(events.first).keys)
        XCTAssertEqual(keys, Set(["id", "category", "title", "summary", "occurredAt", "updatedAt",
                                  "publicationStatus", "isOngoing", "ongoingReason", "ongoingUntil"]))
        let forbidden: Set<String> = ["accessToken", "deviceAccessToken", "authorization", "pairingCode",
                                      "installationId", "pushToken", "raw", "body", "serverURL"]
        XCTAssertTrue(Set(object.keys).union(keys).isDisjoint(with: forbidden))
    }

    func testWatchBuilderUnpairedSnapshotOmitsCachedEventsAndSyncMetadata() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let cached = event("cached-tsunami", category: "tsunami")
        context.insert(cached)
        context.insert(report("cached-warning", eventID: cached.id, sequence: 1,
            type: "VTSE41", category: "tsunami", sections: [area("合成沿岸", kind: "津波警報")]))
        try context.save()

        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: false,
            lastSyncAt: now, receiverSummary: "以前のサーバーの受信状態", now: now)
        XCTAssertFalse(snapshot.isPaired)
        XCTAssertEqual(snapshot.generatedAt, now)
        XCTAssertTrue(snapshot.events.isEmpty)
        XCTAssertNil(snapshot.lastSyncAt)
        XCTAssertNil(snapshot.receiverSummary)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<EventEntity>()), 1)
    }

    func testWatchBuilderRetainsOldOngoingTsunamiOutsideRecentHistoryWindow() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let ongoing = event("old-ongoing-tsunami", category: "tsunami")
        ongoing.latestRevision = 1
        ongoing.latestReportAt = now.addingTimeInterval(-86_400)
        context.insert(ongoing)
        context.insert(report("old-warning", eventID: ongoing.id, sequence: 1,
            type: "VTSE41", category: "tsunami",
            occurredAt: ServerDateParser.string(from: ongoing.latestReportAt),
            sections: [area("合成沿岸", kind: "津波注意報")]))
        for i in 0..<20 {
            let history = event("tsunami-history-\(i)", category: "tsunami")
            history.latestRevision = i + 2
            history.latestReportAt = now.addingTimeInterval(-Double(i + 1))
            context.insert(history)
            context.insert(report("tsunami-observation-\(i)", eventID: history.id,
                sequence: Int64(i + 2), type: "VTSE51", category: "tsunami",
                occurredAt: ServerDateParser.string(from: history.latestReportAt)))
        }
        try context.save()

        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: true,
            lastSyncAt: now, receiverSummary: "受信済み", now: now)
        let retained = try XCTUnwrap(snapshot.events.first { $0.id == ongoing.id })
        XCTAssertTrue(retained.isOngoing(at: now))
        XCTAssertEqual(snapshot.events.filter { $0.category == .tsunami }.count, 16)
        XCTAssertTrue(snapshot.events.contains { $0.id == "tsunami-history-0" })
        XCTAssertFalse(snapshot.events.contains { $0.id == "tsunami-history-19" })
        XCTAssertTrue(snapshot.isPaired)
    }

    func testWatchBuilderBoundsEncodedDataWhileRetainingOngoingAndRecentBothCategories() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        // Multi-scalar graphemes exercise the byte budget even though the
        // display strings are also limited by Swift Character count.
        let largeBody = String(repeating: "👨‍👩‍👧‍👦", count: 300)
        let largeTitle = String(repeating: "合成資料", count: 100)
        var sequence: Int64 = 0
        for category in ["earthquake", "tsunami"] {
            let ongoing = event("ongoing-\(category)", category: category)
            sequence += 1
            ongoing.latestRevision = Int(sequence)
            ongoing.latestReportAt = now.addingTimeInterval(category == "tsunami" ? -86_400 : -10)
            ongoing.title = largeTitle
            context.insert(ongoing)
            let active = report("active-\(category)", eventID: ongoing.id, sequence: sequence,
                type: category == "tsunami" ? "VTSE41" : "VXSE45", category: category,
                revision: category == "earthquake" ? 1 : nil,
                occurredAt: ServerDateParser.string(from: ongoing.latestReportAt),
                sections: category == "tsunami" ? [area("合成沿岸", kind: "津波注意報")] : [])
            active.body = largeBody
            context.insert(active)
            for i in 0..<15 {
                sequence += 1
                let history = event("recent-\(category)-\(i)", category: category)
                history.latestRevision = Int(sequence)
                history.latestReportAt = now.addingTimeInterval(-Double(i + (category == "earthquake" ? 1 : 3_600)))
                history.title = largeTitle
                context.insert(history)
                let historical = report("historical-\(category)-\(i)", eventID: history.id, sequence: sequence,
                    type: category == "tsunami" ? "VTSE51" : "VXSE53", category: category,
                    occurredAt: ServerDateParser.string(from: history.latestReportAt))
                historical.body = largeBody
                context.insert(historical)
            }
        }
        try context.save()

        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: true,
            lastSyncAt: now, receiverSummary: "受信済み", now: now)
        XCTAssertLessThanOrEqual(try snapshot.encoded().count, 48_000)
        XCTAssertLessThan(snapshot.events.count, 32, "The fixture must exercise history trimming.")
        let activeIDs = Set(snapshot.events.filter { $0.isOngoing(at: now) }.map(\.id))
        XCTAssertEqual(activeIDs, Set(["ongoing-earthquake", "ongoing-tsunami"]))
        XCTAssertTrue(snapshot.events.contains { $0.id == "recent-earthquake-0" })
        XCTAssertTrue(snapshot.events.contains { $0.id == "recent-tsunami-0" })
        XCTAssertEqual(try WatchSnapshot.decode(snapshot.encoded()), snapshot)
    }

    func testWatchBuilderShowsWhyEEWAndTsunamiPublicationsEnded() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let earthquake = event("ended-eew", category: "earthquake", type: "eew_warning")
        earthquake.latestRevision = 1
        context.insert(earthquake)
        context.insert(report("final-forecast", eventID: earthquake.id, sequence: 1,
            type: "VXSE45", category: "earthquake", revision: 3, final: true))
        let tsunami = event("released-tsunami", category: "tsunami")
        tsunami.latestRevision = 3
        context.insert(tsunami)
        context.insert(report("tsunami-warning", eventID: tsunami.id, sequence: 2,
            type: "VTSE41", category: "tsunami", sections: [area("合成沿岸", kind: "津波警報")]))
        context.insert(report("tsunami-release", eventID: tsunami.id, sequence: 3,
            type: "VTSE41", category: "tsunami", occurredAt: "2026-10-17T03:00:10Z",
            sections: [area("合成沿岸", kind: "津波警報解除", previous: "津波警報")]))
        try context.save()

        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: true,
            lastSyncAt: now, receiverSummary: nil, now: now)
        let endedEEW = try XCTUnwrap(snapshot.events.first { $0.id == earthquake.id })
        let released = try XCTUnwrap(snapshot.events.first { $0.id == tsunami.id })
        XCTAssertFalse(endedEEW.isOngoing(at: now))
        XCTAssertTrue(endedEEW.publicationStatus.contains("最終報"))
        XCTAssertTrue(endedEEW.ongoingReason?.contains("最終報") == true)
        XCTAssertNil(endedEEW.ongoingUntil)
        XCTAssertFalse(released.isOngoing(at: now))
        XCTAssertTrue(released.publicationStatus.contains("解除"))
        XCTAssertTrue(released.ongoingReason?.contains("解除") == true)
        XCTAssertNil(released.ongoingUntil)
    }

    func testWatchBuilderKeepsCancellationDistinctFromRelease() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let cancelled = event("cancelled-tsunami", category: "tsunami")
        cancelled.latestRevision = 1
        context.insert(cancelled)
        context.insert(report("cancelled-publication", eventID: cancelled.id, sequence: 1,
            type: "VTSE41", category: "tsunami", cancelled: true))
        try context.save()

        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: true,
            lastSyncAt: nil, receiverSummary: nil, now: now)
        let display = try XCTUnwrap(snapshot.events.first)
        XCTAssertFalse(display.isOngoing(at: now))
        XCTAssertEqual(display.publicationStatus, "継続状況未確認")
        XCTAssertTrue(display.ongoingReason?.contains("解除を意味しません") == true)
    }
}
