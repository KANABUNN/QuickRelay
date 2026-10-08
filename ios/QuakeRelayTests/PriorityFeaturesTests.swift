import XCTest
import SwiftUI
import UIKit
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
    func testActualServerActivityFixtureDecodesAsActivityKitContent() throws {
        let resource = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "live-activity.valid", withExtension: "json"))
        let data = try Data(contentsOf: resource)
        let payload = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        let aps = payload["aps"] as! [String: Any]
        let state = try JSONDecoder().decode(QuickRelayActivityAttributes.ContentState.self,
                    from: JSONSerialization.data(withJSONObject: aps["content-state"]!))
        let attributes = try JSONDecoder().decode(QuickRelayActivityAttributes.self,
                    from: JSONSerialization.data(withJSONObject: aps["attributes"]!))
        XCTAssertEqual(state.reportLabel, "第1報")
        XCTAssertEqual(state.intensityText, "最大震度 5弱")
        XCTAssertEqual(attributes.startSequence, 1)
        XCTAssertEqual(attributes.eventID, "20261005000000")
    }
    func testLockScreenCardFitsSystemHeightAndRendersLongStaleContent() throws {
        let state = QuickRelayActivityAttributes.ContentState(
            title: "緊急地震速報（予報）第20報・長い表示タイトルの合成確認",
            summary: "仮定震源の参考値です。実際の震源要素を示す値ではありません。",
            statusText: "続報を待機", intensityText: "最大震度 6強", reportLabel: "第20報",
            category: "earthquake", warning: true, ended: false, cancelled: false,
            reportedAt: 1791414000, updatedAt: 1791414001)
        for width in [320.0, 371.0, 408.0] {
            let view = QuickRelayLiveActivityCard(state: state, isStale: true)
            let host = UIHostingController(rootView: view)
            let size = host.sizeThatFits(in: CGSize(width: width, height: 1000))
            XCTAssertLessThanOrEqual(size.height, 160, "Lock Screen card would be clipped")
            let renderer = ImageRenderer(content: view.frame(width: width))
            renderer.scale = 2
            let image = try XCTUnwrap(renderer.uiImage)
            let attachment = XCTAttachment(image: image)
            attachment.name = "Live Activity stale card \(Int(width))pt"
            attachment.lifetime = .keepAlways
            add(attachment)
        }
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
