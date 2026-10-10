import Foundation
import XCTest
@testable import QuakeRelay

@MainActor
final class MediumFeaturesTests: XCTestCase {
    private func report(_ id: String, sequence: Int64, serial: Int? = nil, type: String = "VTSE41",
                        time: String = "2026-01-01T01:00:00Z", cancelled: Bool = false,
                        infoType: String = "発表", intensity: String? = nil,
                        hypocenter: HypocenterDTO? = nil, sections: [BulletinSectionDTO] = []) -> ReportEntity {
        var dto = ReportDTO(id: id, eventId: "event", serverSequence: UInt64(sequence), messageId: id,
            eventType: type.hasPrefix("VXSE4") ? "eew_forecast" : "tsunami_info", revision: serial,
            isFinal: false, isCancelled: cancelled, title: "合成テスト", body: "実際の発表ではありません。",
            occurredAt: time, receivedAt: time, createdAt: nil)
        dto.telegramType = type; dto.infoType = infoType; dto.maxIntensity = intensity
        dto.hypocenter = hypocenter; dto.bulletin = BulletinDTO(sections: sections)
        return ReportEntity(dto: dto, serverSequence: sequence)
    }
    private func area(_ name: String, kind: String, previous: String? = nil, height: String? = nil) -> BulletinSectionDTO {
        var rows = [BulletinRowDTO(label: "発表", value: kind)]
        if let previous { rows.append(BulletinRowDTO(label: "前回", value: previous)) }
        if let height { rows.append(BulletinRowDTO(label: "予想される高さ", value: height)) }
        return BulletinSectionDTO(title: "津波予報区：" + name, rows: rows)
    }
    func testReadAndPinPersistPerServerAndNewVersionBecomesUnread() {
        let suite = "test.history." + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let history = HistoryPreferences(defaults: defaults)
        let event = EventEntity(dto: TestFixtures.event(latestRevision: 4))
        XCTAssertTrue(history.isUnread(event, scope: "a"))
        history.markRead(event, scope: "a"); history.togglePinned(event.id, scope: "a")
        let reloaded = HistoryPreferences(defaults: defaults)
        XCTAssertFalse(reloaded.isUnread(event, scope: "a"))
        XCTAssertTrue(reloaded.isPinned(event.id, scope: "a"))
        XCTAssertFalse(reloaded.isPinned(event.id, scope: "b"))
        XCTAssertTrue(reloaded.isUnread(event, scope: "b"))
        event.latestRevision = 5
        XCTAssertTrue(reloaded.isUnread(event, scope: "a"))
        reloaded.markAllRead([event], scope: "a")
        XCTAssertFalse(reloaded.isUnread(event, scope: "a"))
    }
    func testHistorySearchTypeIntensityAndInclusiveLocalDate() throws {
        let event = EventEntity(dto: TestFixtures.event(maxIntensity: "5-〜6+"))
        var filter = HistoryFilter()
        filter.search = "日向灘  宮崎県"; filter.minimumIntensity = "6弱"; filter.eventType = "eew_warning"
        XCTAssertTrue(filter.matches(event, searchText: "宮崎県 / 日向灘", unread: true, pinned: false))
        filter.unreadOnly = true
        XCTAssertFalse(filter.matches(event, searchText: "宮崎県 / 日向灘", unread: false, pinned: true))
        filter.unreadOnly = false; filter.pinnedOnly = true
        XCTAssertFalse(filter.matches(event, searchText: "宮崎県 / 日向灘", unread: true, pinned: false))
        filter.pinnedOnly = false; filter.dateRangeEnabled = true
        filter.startDate = event.latestReportAt; filter.endDate = event.latestReportAt
        XCTAssertTrue(filter.matches(event, searchText: "宮崎県 / 日向灘", unread: true, pinned: false))
        event.maxIntensity = nil
        XCTAssertFalse(filter.matches(event, searchText: "宮崎県 / 日向灘", unread: true, pinned: false))
        XCTAssertNil(IntensityValue.rank("不明"))
        XCTAssertEqual(IntensityValue.rank("5-〜6+"), 8)
    }
    func testComparisonStaysInSameTelegramSeriesAndRejectsLateOlderSerial() {
        let old = report("old", sequence: 1, serial: 2, type: "VXSE45")
        let different = report("other", sequence: 2, serial: 8, type: "VXSE43")
        let higher = report("higher", sequence: 3, serial: 9, type: "VXSE45")
        let current = report("now", sequence: 4, serial: 3, type: "VXSE45", time: "2026-01-01T01:00:01Z")
        XCTAssertEqual(ReportComparison.previous(to: current, in: [old, different, higher])?.id, old.id)
        XCTAssertTrue(ReportComparison.changes(from: different, to: current).isEmpty)
    }
    func testComparisonDistinguishesForecastObservationAndCancellation() {
        let previous = report("old", sequence: 1, serial: 1, type: "VXSE45", intensity: "3",
                              hypocenter: HypocenterDTO(status: "estimated", epicenter: "合成震源", magnitude: 4))
        let current = report("now", sequence: 2, serial: 2, type: "VXSE45", intensity: "4",
                             hypocenter: HypocenterDTO(status: "assumed", epicenter: "仮定震源", magnitude: 5))
        let changes = ReportComparison.changes(from: previous, to: current)
        XCTAssertEqual(changes.first(where: { $0.label == "予想最大震度" })?.current, "4")
        XCTAssertTrue(changes.contains(where: { $0.label == "震源の精度" }))
        let cancelled = report("cancel", sequence: 3, serial: 2, type: "VXSE45", cancelled: true)
        let terminal = ReportComparison.changes(from: current, to: cancelled)
        XCTAssertTrue(terminal.contains(where: { $0.label == "取消" }))
        XCTAssertFalse(terminal.contains(where: { $0.label == "マグニチュード" }))
    }
    func testTsunamiAllReleasePartialReleaseCancellationAndMissingDataDiffer() {
        let released = report("release", sequence: 1, sections: [area("宮崎県", kind: "津波注意報解除", previous: "津波注意報")])
        XCTAssertEqual(TsunamiPublication(report: released).status, "警報・注意報の解除を発表")
        let partial = report("partial", sequence: 2, sections: [area("宮崎県", kind: "津波注意報解除"), area("高知県", kind: "津波警報", height: "3 m")])
        XCTAssertEqual(TsunamiPublication(report: partial).status, "一部地域で解除・他地域に警報や注意報")
        XCTAssertEqual(TsunamiPublication(report: report("cancel", sequence: 3, cancelled: true)).status, "情報取消（解除ではありません）")
        XCTAssertNil(TsunamiPublication(report: report("missing", sequence: 4)).status)
        let seaChange = report("sea", sequence: 5, sections: [area("宮崎県", kind: "津波予報（若干の海面変動）", previous: "津波警報")])
        XCTAssertEqual(TsunamiPublication(report: seaChange).status, "警報・注意報の解除を発表")
    }
    func testLateOriginalAndObservationsCannotReplaceTsunamiCancellationState() {
        let cancel = report("cancel", sequence: 2, cancelled: true)
        let original = report("late", sequence: 3, sections: [area("宮崎県", kind: "津波警報")])
        let observation = report("observed", sequence: 4, type: "VTSE51", time: "2026-01-01T02:00:00Z")
        XCTAssertEqual(TsunamiPublication.latest(in: [cancel, original, observation], eventID: "event")?.id, "cancel")
    }
    func testTsunamiHeightAndTargetAreaChangesNeverTurnMissingHeightIntoZero() {
        let old = report("old", sequence: 1, sections: [area("宮崎県", kind: "津波警報", height: "3 m")])
        let new = report("new", sequence: 2, sections: [area("宮崎県", kind: "津波警報解除"),area("高知県", kind: "津波注意報", height: "1 m")])
        let changes = ReportComparison.changes(from: old, to: new)
        XCTAssertEqual(changes.first(where: { $0.label == "津波予報区：宮崎県 / 予想される高さ" })?.current, "記載なし")
        XCTAssertTrue(changes.contains(where: { $0.label == "津波予報区：高知県 / 発表" }))
    }
    func testMapStationLookupUsesHierarchyAndDoesNotGuessAmbiguousNames() {
        let section = BulletinSectionDTO(title: "観測 / 合成県 / 合成地方 / 合成市 / 合成観測点＊", rows: [BulletinRowDTO(label: "震度", value: "4")])
        let current = report("observed", sequence: 1, type: "VXSE53", sections: [section])
        let a = MapStationDTO(code: "a", name: "合成観測点", regionName: "合成地方", cityName: "合成市", latitude: 35, longitude: 139, status: "現")
        let b = MapStationDTO(code: "b", name: "合成観測点", regionName: "別地方", cityName: "別市", latitude: 36, longitude: 140, status: "現")
        func catalog(_ items: [MapStationDTO]) -> StationCatalogResponse {
            StationCatalogResponse(ok: true, version: "synthetic", changeTime: "2025-01-01T00:00:00Z", fetchedAt: "2026-01-01T00:00:00Z", stale: false, items: items)
        }
        let mapped = MapDataBuilder.observations(current, catalog: catalog([a, b]))
        XCTAssertEqual(mapped.points.count, 1); XCTAssertEqual(mapped.unmapped, 0)
        XCTAssertEqual(mapped.points.first?.intensity, "4")
        let duplicate = MapStationDTO(code: "c", name: a.name, regionName: a.regionName, cityName: a.cityName, latitude: 34, longitude: 138, status: "現")
        XCTAssertEqual(MapDataBuilder.observations(current, catalog: catalog([a, duplicate])).unmapped, 1)
        XCTAssertEqual(MapDataBuilder.observations(report("eew", sequence: 2, type: "VXSE45", sections: [section]), catalog: catalog([a])).points.count, 0)
        XCTAssertFalse(MapDataBuilder.valid(latitude: .nan, longitude: 139))
        XCTAssertFalse(MapDataBuilder.valid(latitude: 91, longitude: 139))
    }
    func testMapUsesParameterCutoverAndSuppressesCancelledObservationLocations() {
        let section = BulletinSectionDTO(title: "観測 / 合成点", rows: [BulletinRowDTO(label: "震度", value: "3")])
        let old = MapStationDTO(code: "a", name: "合成点", regionName: "", cityName: "", latitude: 35, longitude: 139, status: "現")
        let changed = MapStationDTO(code: "a", name: "合成点", regionName: "", cityName: "", latitude: 36, longitude: 140, status: "変更")
        let catalog = StationCatalogResponse(ok: true, version: "synthetic", changeTime: "2026-01-01T00:00:00Z", fetchedAt: "2026-01-02T00:00:00Z", stale: false, items: [old, changed])
        XCTAssertEqual(MapDataBuilder.observations(report("before", sequence: 1, type: "VXSE53", time: "2025-12-31T00:00:00Z", sections: [section]), catalog: catalog).points.first?.coordinate.latitude, 35)
        XCTAssertEqual(MapDataBuilder.observations(report("after", sequence: 2, type: "VXSE53", sections: [section]), catalog: catalog).points.first?.coordinate.latitude, 36)
        XCTAssertTrue(MapDataBuilder.observations(report("cancel", sequence: 3, type: "VXSE53", cancelled: true, sections: [section]), catalog: catalog).points.isEmpty)
    }
    func testBundledJmaCoastsMatchAll66RegionsAndPreserveWarningBasis() throws {
        let catalog = try XCTUnwrap(CoastCatalog.load())
        XCTAssertEqual(catalog.regions.count, 66)
        XCTAssertEqual(Set(catalog.regions.map(\.code)).count, 66)
        XCTAssertTrue(catalog.regions.allSatisfy { !$0.coordinates.isEmpty })
        let current = report("tsunami", sequence: 1, sections: [area("宮崎県", kind: "津波警報", height: "3 m")])
        let mapped = MapDataBuilder.coasts(current, catalog: catalog)
        XCTAssertEqual(mapped.count, 1); XCTAssertEqual(mapped.first?.kind, "津波警報")
        XCTAssertEqual(mapped.first?.height, "3 m")
        XCTAssertTrue(MapDataBuilder.coasts(report("cancel", sequence: 2, cancelled: true, sections: current.bulletin?.sections ?? []), catalog: catalog).isEmpty)
    }
}
