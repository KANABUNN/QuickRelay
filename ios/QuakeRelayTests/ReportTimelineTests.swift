import XCTest
@testable import QuakeRelay

@MainActor
final class ReportTimelineTests: XCTestCase {
    private func sourceReport(sequence: Int64, revision: Int, type: String = "VXSE45",
                              eventID: String = "event", cancelled: Bool = false, final: Bool = false) -> ReportEntity {
        var dto = ReportDTO(id: "report-\(sequence)", eventId: eventID, serverSequence: UInt64(sequence),
                            messageId: "message-\(sequence)", eventType: cancelled ? "eew_cancel" : ((type == "VXSE45" || type == "VXSE44") ? "eew_forecast" : "eew_warning"),
                            revision: revision, isFinal: final || cancelled, isCancelled: cancelled, title: "合成テスト", body: "合成データ",
                            occurredAt: "2026-10-07T03:00:00Z", receivedAt: "2026-10-07T03:00:01Z", createdAt: nil)
        dto.classification = (type == "VXSE45" || type == "VXSE44") ? "eew.forecast" : "eew.warning"
        dto.telegramType = type
        if !cancelled { dto.hypocenter = HypocenterDTO(status: "assumed", depthKm: 10, magnitude: 1) }
        return ReportEntity(dto: dto, serverSequence: sequence)
    }

    func testTimelineHidesLegacyDuplicatesButKeepsWarningsAndOtherEvents() {
        let legacy = sourceReport(sequence: 1, revision: 99, type: "VXSE44")
        let modern = sourceReport(sequence: 2, revision: 1)
        let warning = sourceReport(sequence: 3, revision: 1, type: "VXSE43")
        let cancel = sourceReport(sequence: 4, revision: 1, cancelled: true)
        let otherLegacy = sourceReport(sequence: 5, revision: 1, type: "VXSE44", eventID: "other")
        let visible = ReportTimelineDisplay.visible([legacy, modern, warning, cancel, otherLegacy])
        XCTAssertEqual(visible.map(\.id), [modern.id, warning.id, cancel.id, otherLegacy.id])
        XCTAssertEqual(ReportTimelineDisplay.visible([legacy]).map(\.id), [legacy.id])
    }

    func testForecastReferenceRejectsOlderRevisionAndOtherEventOrProduct() {
        let latest = sourceReport(sequence: 2, revision: 3)
        let reports = [sourceReport(sequence: 1, revision: 1), latest,
                       sourceReport(sequence: 3, revision: 2),
                       sourceReport(sequence: 4, revision: 99, type: "VXSE43"),
                       sourceReport(sequence: 5, revision: 99, eventID: "other")]
        let selected = ForecastReference.latest(eventID: "event", reports: Array(reports.reversed()))
        XCTAssertEqual(selected?.id, latest.id)
        XCTAssertEqual(selected?.numericHypocenter?.magnitude, 1)
        XCTAssertTrue(selected?.numericHypocenter?.isAssumed == true)
        XCTAssertEqual(selected?.telegramType, "VXSE45")
    }

    func testForecastReferenceNeverComparesVXSE44AndVXSE45Revisions() {
        let preferred = sourceReport(sequence: 2, revision: 3)
        let oldProduct = sourceReport(sequence: 3, revision: 99, type: "VXSE44")
        let cancellation = sourceReport(sequence: 4, revision: 99, type: "VXSE44", cancelled: true)
        XCTAssertEqual(ForecastReference.latest(eventID: "event",
            reports: [oldProduct, cancellation, preferred])?.id, preferred.id)
    }

    func testCancelledVXSE45DoesNotFallBackToUncancelledVXSE44() {
        let reports = [sourceReport(sequence: 1, revision: 3),
                       sourceReport(sequence: 2, revision: 3, cancelled: true),
                       sourceReport(sequence: 3, revision: 99, type: "VXSE44")]
        XCTAssertNil(ForecastReference.latest(eventID: "event", reports: reports))
    }

    func testVXSE44ReferenceIsAvailableWithoutVXSE45ButRemainsTerminal() {
        let final = sourceReport(sequence: 2, revision: 3, type: "VXSE44", final: true)
        let reports = [sourceReport(sequence: 1, revision: 1, type: "VXSE44"), final,
                       sourceReport(sequence: 3, revision: 4, type: "VXSE44")]
        XCTAssertEqual(ForecastReference.latest(eventID: "event", reports: reports)?.id, final.id)
        XCTAssertNil(ForecastReference.latest(eventID: "event", reports: reports +
            [sourceReport(sequence: 4, revision: 3, type: "VXSE44", cancelled: true)]))
    }

    func testUnknownForecastProductIsNotUsedAsReference() {
        let unknown = sourceReport(sequence: 1, revision: 99, type: "VXSE42")
        unknown.classification = "eew.forecast"
        XCTAssertNil(ForecastReference.latest(eventID: "event", reports: [unknown]))
    }

    func testCancelledForecastCannotReappearAsReference() {
        let reports = [sourceReport(sequence: 1, revision: 3),
                       sourceReport(sequence: 2, revision: 3, cancelled: true),
                       sourceReport(sequence: 3, revision: 4),
                       sourceReport(sequence: 4, revision: 2)]
        XCTAssertNil(ForecastReference.latest(eventID: "event", reports: reports))
    }

    func testFinalForecastSurvivesLaterNonfinalAndCanBeCancelled() {
        let reports = [sourceReport(sequence: 1, revision: 3, final: true),
                       sourceReport(sequence: 2, revision: 4)]
        XCTAssertEqual(ForecastReference.latest(eventID: "event", reports: reports)?.revision, 3)
        XCTAssertNil(ForecastReference.latest(eventID: "event", reports: reports + [sourceReport(sequence: 3, revision: 3, cancelled: true)]))
    }

    func testWarningCancellationDoesNotCancelForecastReference() {
        let reports = [sourceReport(sequence: 1, revision: 3),
                       sourceReport(sequence: 2, revision: 3, type: "VXSE43", cancelled: true)]
        XCTAssertEqual(ForecastReference.latest(eventID: "event", reports: reports)?.revision, 3)
    }

    func testForecastWarningPromotionAtSameSerialAndTimeUpdatesReference() {
        let forecast = sourceReport(sequence: 1, revision: 3)
        let promoted = sourceReport(sequence: 2, revision: 3)
        forecast.isWarning = false
        promoted.isWarning = true
        XCTAssertEqual(ForecastReference.latest(eventID: "event", reports: [forecast, promoted])?.id, promoted.id)
    }

    func testTimelineSortsKnownRevisionsAscendingAndSameRevisionBySequence() {
        let revisionFour = ReportEntity(
            dto: TestFixtures.report(id: "rpt_4", sequence: 40, revision: 4),
            serverSequence: 40
        )
        let revisionTwoLateDuplicate = ReportEntity(
            dto: TestFixtures.report(id: "rpt_2b", sequence: 30, revision: 2),
            serverSequence: 30
        )
        let revisionTwo = ReportEntity(
            dto: TestFixtures.report(id: "rpt_2", sequence: 20, revision: 2),
            serverSequence: 20
        )

        let result = [revisionFour, revisionTwoLateDuplicate, revisionTwo]
            .sorted(by: ReportTimelineOrder.areInAscendingOrder)

        XCTAssertEqual(result.map(\.id), ["rpt_2", "rpt_2b", "rpt_4"])
    }

    func testEqualTimeReportsUseSequenceAcrossProducts() {
        let revisionOne = ReportEntity(
            dto: TestFixtures.report(id: "rpt_1", sequence: 2, revision: 1),
            serverSequence: 2
        )
        let revisionless = ReportEntity(
            dto: TestFixtures.report(id: "rpt_generic", sequence: 1, revision: nil),
            serverSequence: 1
        )

        let result = [revisionless, revisionOne]
            .sorted(by: ReportTimelineOrder.areInAscendingOrder)
        XCTAssertEqual(result.map(\.id), ["rpt_generic", "rpt_1"])
    }
}

extension ReportTimelineTests {
    func testOnlyEEWHasNumberedReportsIncludingOldCachedOrdinaryReports() {
        XCTAssertEqual(PublicationLabel.text(eventType: "eew_forecast", serial: 3, infoType: "発表", cancelled: false), "第3報")
        for type in ["earthquake_info", "earthquake_update", "tsunami_info", "tsunami_warning", "nankai_info"] {
            XCTAssertEqual(PublicationLabel.text(eventType: type, serial: 3, infoType: nil, cancelled: false), "発表")
            XCTAssertEqual(PublicationLabel.text(eventType: type, serial: 3, infoType: "訂正", cancelled: false), "訂正")
            XCTAssertEqual(PublicationLabel.text(eventType: type, serial: 3, infoType: "取消", cancelled: true), "取消")
        }
    }

    @MainActor
    func testExpandedPreferencesPreserveExistingOptOutAndApplyOnlyOnce() throws {
        let suite = "expanded-preferences.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let settings = AppSettings(defaults: defaults)
        let old = DevicePreferences(notificationsEnabled: true, timeSensitiveEnabled: false, customSoundEnabled: false, eventTypes: ["eew_warning"])
        settings.apply(old)
        XCTAssertFalse(settings.earthquakeNotificationsEnabled)
        XCTAssertFalse(settings.timeSensitiveEnabled)
        XCTAssertTrue(settings.enabledEventTypes.contains("tsunami_warning"))
        XCTAssertTrue(settings.enabledEventTypes.contains("nankai_info"))
        settings.expandedPreferencesSaved()
        settings.apply(old)
        XCTAssertFalse(settings.tsunamiNotificationsEnabled)
        XCTAssertFalse(settings.advisoryNotificationsEnabled)
        settings.notificationsEnabled = false
        XCTAssertTrue(settings.enabledEventTypes.isEmpty)
        let reopened = AppSettings(defaults: defaults)
        XCTAssertFalse(reopened.needsExpandedPreferences)
        XCTAssertFalse(reopened.tsunamiNotificationsEnabled)
    }
}
