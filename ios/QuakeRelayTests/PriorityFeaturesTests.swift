import XCTest
@testable import QuakeRelay

@MainActor
final class PriorityFeaturesTests: XCTestCase {
    func testReceiverUsesHeartbeatInsteadOfEarthquakeArrivalAndExpiresConfirmation() {
        let now = Date()
        let source = SourceStatusDTO(connected: true, lastFrameAt: ServerDateParser.string(from: now),
                                     lastDataAt: "2020-01-01T00:00:00Z", reconnects: 0, rejected: 0)
        let status = ReceiverStatusResponse(ok: true, source: source, sourceConfigured: true,
                                             sourceFresh: true, db: true, apnsConfigured: true, serverTime: nil)
        XCTAssertEqual(ReceiverState.resolve(status: status, checkedAt: now, failed: false, now: now), .healthy)
        XCTAssertEqual(ReceiverState.resolve(status: status, checkedAt: now, failed: false, now: now.addingTimeInterval(75)), .unchecked)
        XCTAssertEqual(ReceiverState.resolve(status: status, checkedAt: now, failed: true, now: now), .unavailable)
    }
    func testOldStatusDoesNotClaimHealthyAndOfflineIsExplicit() {
        let source = SourceStatusDTO(connected: false, lastFrameAt: "", lastDataAt: "", reconnects: 0, rejected: 0)
        let now = Date()
        let old = ReceiverStatusResponse(ok: true, source: source, sourceConfigured: nil,
                                         sourceFresh: nil, db: nil, apnsConfigured: true, serverTime: nil)
        XCTAssertEqual(ReceiverState.resolve(status: old, checkedAt: now, failed: false, now: now), .unchecked)
        let offline = ReceiverStatusResponse(ok: true, source: source, sourceConfigured: false,
                                             sourceFresh: false, db: true, apnsConfigured: false, serverTime: nil)
        XCTAssertEqual(ReceiverState.resolve(status: offline, checkedAt: now, failed: false, now: now), .offline)
    }
    func testRegionNamesAreTrimmedAndDeduplicatedAndCategorySwitchesIndependent() throws {
        XCTAssertEqual(RegionSelection.parse("宮崎県、 石川県,宮崎県\n石川県加賀"), ["宮崎県","石川県","石川県加賀"])
        let defaults = UserDefaults(suiteName: UUID().uuidString)!
        let settings = AppSettings(defaults: defaults)
        XCTAssertFalse(settings.liveActivitiesEnabled)
        settings.eewForecastEnabled = false
        settings.nankaiEnabled = false
        settings.earthquakeRegionsText = "宮崎県"
        let prefs = settings.devicePreferences
        XCTAssertFalse(prefs.eventTypes.contains("eew_forecast"))
        XCTAssertTrue(prefs.eventTypes.contains("eew_warning"))
        XCTAssertTrue(prefs.eventTypes.contains("eew_cancel"))
        XCTAssertFalse(prefs.eventTypes.contains("nankai_info"))
        XCTAssertTrue(prefs.eventTypes.contains("seismic_advisory"))
        XCTAssertEqual(prefs.earthquakeRegions, ["宮崎県"])
    }
    func testOldDevicePreferencesDecodeWithFiltersOff() throws {
        let data = #"{"notifications_enabled":true,"time_sensitive_enabled":true,"custom_sound_enabled":false,"event_types":["eew_warning"]}"#.data(using: .utf8)!
        let p = try JSONDecoder.quakeRelay.decode(DevicePreferences.self, from: data)
        XCTAssertNil(p.liveActivitiesEnabled)
        XCTAssertNil(p.minimumIntensity)
        XCTAssertNil(p.earthquakeRegions)
    }
    func testActivityContentUsesDefaultCodableKeysAndStaleIsNotAnAllClear() throws {
        let data = #"{"title":"緊急地震速報","summary":"仮定値","statusText":"続報を待機","intensityText":"最大震度 5弱","reportLabel":"第2報","category":"earthquake","warning":true,"ended":false,"cancelled":false,"reportedAt":1791414000,"updatedAt":1791414001}"#.data(using: .utf8)!
        let state = try JSONDecoder().decode(QuickRelayActivityAttributes.ContentState.self, from: data)
        XCTAssertEqual(state.freshnessLabel(isStale: true), "更新が途切れています・最新情報を確認")
        let encoded = try JSONEncoder().encode(state)
        let object = try JSONSerialization.jsonObject(with: encoded) as! [String: Any]
        XCTAssertNotNil(object["statusText"])
        XCTAssertNil(object["status_text"])
        let attributes = QuickRelayActivityAttributes(eventID: "202610080001", telegramType: "VXSE45")
        XCTAssertEqual(DeepLinkParser.parse(url: attributes.detailURL!), .event(id: "202610080001", reportID: nil))
    }
}
