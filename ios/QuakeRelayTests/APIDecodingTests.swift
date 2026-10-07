import XCTest
@testable import QuakeRelay

final class APIDecodingTests: XCTestCase {
    func testAssumedValuesAreQualifiedAndLegacyFieldsStayEmpty() throws {
        let json = #"""
        {"id":"assumed","category":"earthquake","event_type":"eew_forecast",
         "origin_time":null,"epicenter":null,"latitude":null,"longitude":null,
         "depth_km":null,"magnitude":null,"max_intensity":"5弱以上","latest_revision":8,
         "is_final":false,"is_cancelled":false,"latest_report_at":"2026-10-07T03:00:00Z",
         "hypocenter":{"status":"assumed","note":"仮定震源の参考値です。",
           "origin_time":"2026-10-07T02:59:58Z","epicenter":"合成震源",
           "latitude":31.8,"longitude":131.7,"depth_km":10,"magnitude":1.0}}
        """#.data(using: .utf8)!
        let event = try JSONDecoder.quakeRelay.decode(EventDTO.self, from: json)
        XCTAssertNil(event.magnitude)
        XCTAssertNil(event.epicenter)
        let details = try XCTUnwrap(event.hypocenter)
        XCTAssertTrue(details.isAssumed)
        XCTAssertEqual(details.magnitude, 1)
        XCTAssertEqual(details.depthKm, 10)
        XCTAssertEqual(details.label("深さ"), "深さ（仮定値）")
        XCTAssertEqual(details.qualification, "仮定震源の参考値です。")
    }

    func testHypocenterQualifiersDoNotTurnBoundsIntoExactValues() {
        let deep = HypocenterDTO(status: "estimated", depthKm: 700, depthCondition: "７００ｋｍ以上")
        let shallow = HypocenterDTO(status: "estimated", depthKm: 0, depthCondition: "ごく浅い")
        XCTAssertEqual(deep.depthText, "700 km以上")
        XCTAssertEqual(shallow.depthText, "ごく浅い（数値 0 km）")
        XCTAssertNil(HypocenterDTO(status: "assumed").depthText)
        XCTAssertNotNil(HypocenterDTO(status: "assumed").qualification)
        XCTAssertNotNil(HypocenterDTO(status: "low_accuracy").qualification)
    }

    func testDecodesApprovedPagedSyncShape() throws {
        let json = #"""
        {
          "ok": true,
          "items": [{
            "report": {
              "id": "rpt_001",
              "event_id": "evt_001",
              "server_sequence": 12352,
              "message_id": "01JTEST",
              "event_type": "eew_warning",
              "revision": 4,
              "is_final": false,
              "is_cancelled": false,
              "title": "緊急地震速報（警報） 第4報",
              "body": "日向灘 M6.2 最大震度5強",
              "occurred_at": "2026-08-13T13:30:01.123Z",
              "received_at": "2026-08-13T13:30:01.456Z",
              "created_at": "2026-08-13T13:30:02.000Z"
            },
            "event": {
              "id": "evt_001",
              "category": "earthquake",
              "event_type": "eew_warning",
              "origin_time": "2026-08-13T13:29:58.000Z",
              "epicenter": "日向灘",
              "latitude": 31.8,
              "longitude": 131.7,
              "depth_km": 30,
              "magnitude": 6.2,
              "max_intensity": "5強",
              "latest_revision": 12352,
              "source_serial": 4,
              "classification": "eew.warning",
              "telegram_type": "VXSE43",
              "is_warning": true,
              "is_final": false,
              "is_cancelled": false,
              "latest_report_at": "2026-08-13T13:30:05.000Z"
            }
          }],
          "next_after_sequence": 12352,
          "has_more": false,
          "latest_committed_sequence": 12352,
          "server_time": "2026-08-13T13:31:00.000Z"
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder.quakeRelay.decode(SyncResponse.self, from: json)

        XCTAssertTrue(response.ok)
        XCTAssertEqual(response.nextAfterSequence, 12_352)
        XCTAssertFalse(response.hasMore)
        XCTAssertEqual(response.items.first?.event.epicenter, "日向灘")
        XCTAssertEqual(response.items.first?.report.revision, 4)
        XCTAssertEqual(response.items.first?.event.latestRevision, 12352)
        XCTAssertEqual(response.items.first?.event.sourceSerial, 4)
        XCTAssertEqual(response.items.first?.event.classification, "eew.warning")
    }

    func testDecodesOptionalEarthquakeFields() throws {
        let json = #"""
        {
          "id":"evt_unknown_hypocenter",
          "category":"earthquake",
          "event_type":"earthquake_info",
          "origin_time":null,
          "epicenter":null,
          "latitude":null,
          "longitude":null,
          "depth_km":null,
          "magnitude":null,
          "max_intensity":null,
          "latest_revision":null,
          "is_final":false,
          "is_cancelled":false,
          "latest_report_at":"2026-08-13T13:30:05Z"
        }
        """#.data(using: .utf8)!

        let event = try JSONDecoder.quakeRelay.decode(EventDTO.self, from: json)
        XCTAssertNil(event.magnitude)
        XCTAssertNil(event.latestRevision)
        XCTAssertNotNil(ServerDateParser.parse(event.latestReportAt))
    }

    func testDecodesFreshlyPairedDeviceBeforeAPNsRegistration() throws {
        let json = #"""
        {
          "ok": true,
          "device": {
            "installation_id": "1777a91b-13c5-4c8c-9374-1888ead66fac",
            "device_name": null,
            "environment": "sandbox",
            "app_version": null,
            "os_version": null,
            "active": true,
            "preferences": {
              "notifications_enabled": true,
              "time_sensitive_enabled": true,
              "custom_sound_enabled": true,
              "event_types": ["eew_warning", "eew_cancel"]
            },
            "last_seen_at": null
          }
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder.quakeRelay.decode(DeviceResponse.self, from: json)
        XCTAssertNil(response.device.deviceName)
        XCTAssertNil(response.device.appVersion)
        XCTAssertEqual(response.device.preferences.eventTypes, ["eew_warning", "eew_cancel"])
    }

    func testDecodesCompleteSanitizedHealthResponse() throws {
        let json = #"""
        {
          "ok": true,
          "version": "1.0.0",
          "db": true,
          "apns_http2": true,
          "config_loaded": true,
          "migration_current": true,
          "time": "2026-08-13T13:31:00.000Z"
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder.quakeRelay.decode(HealthResponse.self, from: json)
        XCTAssertTrue(response.configLoaded)
        XCTAssertTrue(response.migrationCurrent)
    }
}
