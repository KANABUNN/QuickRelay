import Foundation
import SwiftData
import XCTest
@testable import QuakeRelay

/// Display regrouping must neither drop nor invent bulletin content.
@MainActor
final class PresentationTests: XCTestCase {
    private let now = ServerDateParser.parse("2026-11-20T03:00:30Z")!

    private func report(_ id: String, eventID: String = "quake", sequence: Int64, type: String,
                        category: String = "earthquake", revision: Int? = nil, cancelled: Bool = false,
                        final: Bool = false, occurredAt: String = "2026-11-20T03:00:00Z",
                        maxIntensity: String? = nil, hypocenter: HypocenterDTO? = nil,
                        areas: [AffectedAreaDTO]? = nil, sections: [BulletinSectionDTO] = []) -> ReportEntity {
        let isEEW = ["VXSE43", "VXSE44", "VXSE45"].contains(type)
        var dto = ReportDTO(id: id, eventId: eventID, serverSequence: UInt64(sequence),
            messageId: "message-" + id,
            eventType: isEEW ? (cancelled ? "eew_cancel" : type == "VXSE43" ? "eew_warning" : "eew_forecast")
                : category == "tsunami" ? "tsunami_info" : "earthquake_info",
            revision: revision, isFinal: final || (isEEW && cancelled), isCancelled: cancelled,
            title: "合成テスト", body: "実際の防災情報ではありません。",
            occurredAt: occurredAt, receivedAt: occurredAt, createdAt: nil)
        dto.category = category
        dto.classification = isEEW ? (type == "VXSE43" ? "eew.warning" : "eew.forecast") : "telegram.earthquake"
        dto.telegramType = type
        dto.infoType = cancelled ? "取消" : "発表"
        dto.isWarning = type == "VXSE43"
        dto.maxIntensity = maxIntensity
        dto.hypocenter = hypocenter
        dto.affectedAreas = areas
        dto.bulletin = BulletinDTO(sections: sections)
        return ReportEntity(dto: dto, serverSequence: sequence)
    }

    private func rows(_ pairs: [(String, String)]) -> [BulletinRowDTO] {
        pairs.map { BulletinRowDTO(label: $0.0, value: $0.1) }
    }

    private var observationSections: [BulletinSectionDTO] {
        [
            BulletinSectionDTO(title: "観測 / 合成県", rows: rows([("最大震度", "4")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 合成地方", rows: rows([("最大震度", "4")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 合成地方 / 合成市", rows: rows([("最大震度", "4")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 合成地方 / 合成市 / 合成観測地点01", rows: rows([("震度", "4")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 合成地方 / 合成市 / 合成観測地点02", rows: rows([("震度", "3")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 別の地方", rows: rows([("最大震度", "2")])),
            BulletinSectionDTO(title: "観測 / 合成県 / 別の地方 / 別の市 / 別の地点", rows: rows([("震度", "2")])),
        ]
    }

    func testIntensityReadingsKeepTheSourceMeaning() throws {
        XCTAssertEqual(IntensityReading("5弱")?.dominant, .fiveLower)
        XCTAssertEqual(IntensityReading("5-")?.dominant, .fiveLower)
        XCTAssertEqual(IntensityReading("６強")?.dominant, .sixUpper)
        XCTAssertEqual(IntensityReading("最大震度 7")?.dominant, .seven)
        let range = try XCTUnwrap(IntensityReading("3〜4"))
        XCTAssertEqual(range.lower, .three)
        XCTAssertEqual(range.upper, .four)
        XCTAssertEqual(range.mainText, "3〜4")
        let open = try XCTUnwrap(IntensityReading("5弱以上"))
        XCTAssertTrue(open.openEnded)
        XCTAssertEqual(open.displayText, "5弱以上")
        XCTAssertTrue(try XCTUnwrap(IntensityReading("5弱以上未入電")).notReceived)
        let unknown = try XCTUnwrap(IntensityReading("不明"))
        XCTAssertNil(unknown.dominant)
        XCTAssertEqual(unknown.mainText, "不明")
        XCTAssertNil(IntensityReading(""))
        XCTAssertNil(IntensityReading(nil))
        XCTAssertGreaterThan(IntensityReading.sortKey(open), IntensityReading.sortKey(IntensityReading("5弱")))
        XCTAssertEqual(LongPeriodClass("2"), .two)
        XCTAssertNil(LongPeriodClass("階級不明"))
    }

    func testBulletinLayoutAssignsEverySectionExactlyOnce() {
        let sections = observationSections + [
            BulletinSectionDTO(title: "津波予報区：合成沿岸", rows: rows([("発表", "津波注意報")])),
            BulletinSectionDTO(title: "津波観測：合成港", rows: rows([("第1波", "到達を確認")])),
            BulletinSectionDTO(title: "沿岸の推定：合成沿岸", rows: rows([("最大波の推定", "0.2m")])),
            BulletinSectionDTO(title: "長周期地震動", rows: rows([("最大長周期地震動階級", "1")])),
            BulletinSectionDTO(title: "関連する地震", rows: rows([("震源", "合成沖")])),
            BulletinSectionDTO(title: "地震回数：1時間ごと", rows: rows([("地震回数", "3")])),
            BulletinSectionDTO(title: "防災上の留意事項", text: "この地震による津波の心配はありません。"),
            BulletinSectionDTO(title: "補足", text: "合成補足"),
            BulletinSectionDTO(title: "未知の項目", text: "推測せずに残す"),
            BulletinSectionDTO(title: "観測", text: "階層を持たない観測"),
        ]
        let layout = BulletinLayout(bulletin: BulletinDTO(headline: "見出し", sections: sections))
        XCTAssertEqual(layout.assignedCount, sections.count)
        let grouped = layout.textItems.count + layout.intensitySections.count + layout.tsunamiForecastSections.count
            + layout.tsunamiObservationSections.count + layout.tsunamiEstimateSections.count
            + layout.relatedEarthquakes.count + layout.earthquakeCounts.count + layout.otherSections.count
            + (layout.longPeriodRows.isEmpty ? 0 : 1)
        XCTAssertEqual(grouped, sections.count, "Every section must be displayed by exactly one group")
        XCTAssertEqual(layout.otherSections.map(\.title), ["未知の項目", "観測"])
        XCTAssertEqual(layout.comments.map(\.text), ["この地震による津波の心配はありません。"])
        XCTAssertEqual(layout.text(.appendix), "合成補足")
        XCTAssertEqual(layout.headline, "見出し")
    }

    func testObservationTreeKeepsEveryLevelAndSummarizesRegions() throws {
        let tree = IntensityObservationTree(sections: observationSections)
        XCTAssertEqual(tree.roots.map(\.name), ["合成県"])
        // "別の市" has no row of its own but is kept as the parent of its station.
        XCTAssertEqual(tree.allNodes.count, observationSections.count + 1)
        XCTAssertEqual(tree.summaryDepth, 2)
        let groups = tree.groups(atDepth: 2)
        XCTAssertEqual(groups.map { $0.reading?.displayText }, ["4", "2"])
        XCTAssertEqual(groups.first?.names, ["合成地方"])
        XCTAssertEqual(Set(tree.leaves.map(\.name)), ["合成観測地点01", "合成観測地点02", "別の地点"])
        // The irregular branch (no municipality row) still appears under its region.
        let anchors = tree.listSections
        XCTAssertEqual(anchors.map(\.name), ["合成地方", "別の地方"])
        let listed = anchors.flatMap { [$0] + $0.descendants }.map(\.id)
        XCTAssertEqual(Set(listed).count, tree.allNodes.count - 1, "All nodes below the prefecture are listed")
        XCTAssertEqual(tree.leafGroups.first?.names, ["合成観測地点01"])
    }

    func testEarthquakeSummaryUsesLatestHypocenterAndSkipsCancelledProducts() throws {
        let first = report("vxse53", sequence: 1, type: "VXSE53", maxIntensity: "4",
            hypocenter: HypocenterDTO(status: "estimated", epicenter: "合成震源A", magnitude: 4.1),
            sections: observationSections)
        let update = report("vxse61", sequence: 2, type: "VXSE61", occurredAt: "2026-11-20T03:30:00Z",
            hypocenter: HypocenterDTO(status: "estimated", epicenter: "合成震源B", magnitude: 4.3))
        var summary = EarthquakeEventSummary(reports: [first, update])
        XCTAssertEqual(summary.hypocenterReport?.id, "vxse61")
        XCTAssertEqual(summary.observedReport?.id, "vxse53")
        XCTAssertEqual(summary.observedMax?.dominant, .four)
        XCTAssertTrue(summary.cancelledTypes.isEmpty)

        let cancelled = report("vxse53-cancel", sequence: 3, type: "VXSE53", cancelled: true,
                               occurredAt: "2026-11-20T03:40:00Z")
        summary = EarthquakeEventSummary(reports: [first, update, cancelled])
        XCTAssertNil(summary.observedReport, "A cancelled publication must not keep showing its values")
        XCTAssertEqual(summary.cancelledTypes, ["VXSE53"])
        XCTAssertEqual(summary.hypocenterReport?.id, "vxse61")
    }

    func testEEWSummaryPrefersForecastValuesAndKeepsStreamsSeparate() {
        let forecast = report("f2", sequence: 2, type: "VXSE45", revision: 2, maxIntensity: "5弱",
            hypocenter: HypocenterDTO(status: "estimated", epicenter: "合成震源", depthKm: 30, magnitude: 5.8),
            areas: [AffectedAreaDTO(name: "合成県", maxIntensity: nil),
                    AffectedAreaDTO(name: "合成地方B", maxIntensity: "4"),
                    AffectedAreaDTO(name: "合成地方A", maxIntensity: "5弱")])
        let older = report("f1", sequence: 1, type: "VXSE45", revision: 1, maxIntensity: "4")
        let warning = report("w1", sequence: 3, type: "VXSE43", revision: 1, maxIntensity: "5弱",
            hypocenter: HypocenterDTO(status: "estimated", epicenter: "合成震源"),
            areas: [AffectedAreaDTO(name: "合成", maxIntensity: nil)])
        let summary = EarthquakeEventSummary(reports: [older, forecast, warning])
        XCTAssertEqual(summary.forecastReport?.id, "f2")
        XCTAssertEqual(summary.warningReport?.id, "w1")
        XCTAssertEqual(summary.hypocenterReport?.id, "f2", "Forecast values are the reference shown with a warning")
        XCTAssertEqual(EEWPresentation.forecastAreas(forecast).map(\.name), ["合成地方A", "合成地方B"])
        XCTAssertEqual(EEWPresentation.namedAreas(warning), ["合成"])
        XCTAssertEqual(EEWPresentation.reports([warning, forecast, older]).map(\.id), ["f1", "f2", "w1"])
    }

    func testTsunamiForecastEntriesSplitStationsAndGroupReleases() throws {
        let section = BulletinSectionDTO(title: "津波予報区：合成沿岸", rows: rows([
            ("発表", "大津波警報"), ("第1波の到達予想", "11/20 12:10 JST"), ("予想される高さ", "巨大"),
            ("合成港 到達予想", "11/20 12:15 JST"), ("合成港 満潮", "11/20 15:00 JST"), ("補足", "合成")]))
        let entry = try XCTUnwrap(TsunamiForecastEntry(section: section))
        XCTAssertEqual(entry.style, .major)
        XCTAssertEqual(entry.maxHeight, "巨大")
        XCTAssertEqual(entry.stations, [TsunamiStationForecast(name: "合成港", arrival: "11/20 12:15 JST", highTide: "11/20 15:00 JST")])
        XCTAssertEqual(entry.otherRows.map(\.label), ["補足"])

        let released = report("t1", eventID: "tsunami", sequence: 1, type: "VTSE41", category: "tsunami", sections: [
            section,
            BulletinSectionDTO(title: "津波予報区：解除の沿岸", rows: rows([("発表", "津波予報（若干の海面変動）"), ("前回", "津波注意報")])),
            BulletinSectionDTO(title: "津波予報区：予報の沿岸", rows: rows([("発表", "津波予報（若干の海面変動）")])),
        ])
        let groups = TsunamiForecastGroup.groups(released)
        XCTAssertEqual(groups.map(\.style), [.major, .forecast, .released])
        XCTAssertEqual(groups.last?.entries.map(\.name), ["解除の沿岸"])
    }

    func testMetadataNeverShowsOrdinarySerialsAsReportNumbers() {
        let ordinary = report("vxse53", sequence: 4, type: "VXSE53", revision: 9)
        let labels = ReportMetadata.rows(for: ordinary)
        XCTAssertFalse(labels.contains { $0.value.contains("第9報") })
        XCTAssertEqual(labels.first { $0.label == "通番（Serial）" }?.value, "9")
        let eew = report("eew", sequence: 5, type: "VXSE45", revision: 3)
        XCTAssertEqual(ReportMetadata.rows(for: eew).first { $0.label == "報数" }?.value, "第3報")
    }

    func testTelegramCatalogNamesEveryContractedProduct() {
        let codes = ["VXSE43", "VXSE44", "VXSE45", "VXSE51", "VXSE52", "VXSE53", "VXSE56", "VXSE60", "VXSE61", "VXSE62",
                     "VTSE41", "VTSE51", "VTSE52", "VYSE50", "VYSE51", "VYSE52", "VYSE60", "VZSE40", "WEPA60", "IXAC41"]
        for code in codes {
            XCTAssertNotNil(TelegramCatalog.name(code), code)
            XCTAssertNotNil(TelegramCatalog.short(code), code)
        }
        XCTAssertNil(TelegramCatalog.name("VXSE42"))
    }

    func testWatchDetailIsOptionalAndRoundTrips() throws {
        let plain = WatchEventSnapshot(id: "quake", category: .earthquake, title: "合成震源", summary: "",
            occurredAt: now, updatedAt: now, publicationStatus: "発表", isOngoing: false, ongoingReason: nil)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(plain)) as? [String: Any])
        XCTAssertNil(object["detail"], "Absent detail keeps the previous wire format")
        var rich = plain
        rich.detail = WatchEventDetail(kindLabel: "震源・震度情報 · 発表", intensity: "5弱", originTime: now,
            magnitude: "M5.8", depth: "30 km",
            areas: [WatchAreaIntensity(name: "合成地方", intensity: "5弱")],
            tsunamiLevels: [WatchTsunamiLevel(title: "津波注意報", areas: [WatchTsunamiArea(name: "合成沿岸", height: "1m")])])
        let snapshot = WatchSnapshot(generatedAt: now, lastSyncAt: now, receiverSummary: nil, events: [rich])
        XCTAssertEqual(try WatchSnapshot.decode(snapshot.encoded()), snapshot)
    }

    func testWatchBuilderAddsStructuredEarthquakeValues() throws {
        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        let event = EventEntity(dto: EventDTO(id: "quake", category: "earthquake", eventType: "earthquake_info",
            originTime: "2026-11-20T02:59:50Z", epicenter: "合成震源", latitude: nil, longitude: nil,
            depthKm: 10, magnitude: 4.1, maxIntensity: "4", latestRevision: 1,
            isFinal: false, isCancelled: false, latestReportAt: "2026-11-20T03:00:00Z"))
        context.insert(event)
        context.insert(report("vxse53", sequence: 1, type: "VXSE53", maxIntensity: "4",
            hypocenter: HypocenterDTO(status: "estimated", originTime: "2026-11-20T02:59:50Z",
                                      epicenter: "合成震源", depthKm: 10, magnitude: 4.1),
            sections: observationSections + [BulletinSectionDTO(title: "防災上の留意事項", text: "この地震による津波の心配はありません。")]))
        try context.save()
        let snapshot = try WatchSnapshotBuilder.make(context: context, paired: true, lastSyncAt: now,
                                                     receiverSummary: nil, now: now)
        let detail = try XCTUnwrap(snapshot.events.first?.detail)
        XCTAssertEqual(detail.intensity, "4")
        XCTAssertNil(detail.intensityIsForecast)
        XCTAssertEqual(detail.magnitude, "M4.1")
        XCTAssertEqual(detail.depth, "10 km")
        XCTAssertEqual(detail.comment, "この地震による津波の心配はありません。")
        XCTAssertEqual(detail.areas?.map(\.name), ["合成地方", "別の地方"])
    }
}
