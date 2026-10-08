import Foundation
import SwiftData
import XCTest
@testable import QuakeRelay

/// Opt-in acceptance against a disposable Go process and read-only public VPS URLs.
/// No real APNs token, Apple account, production pairing or private key is used.
@MainActor
final class ServerAcceptanceTests: XCTestCase {
    private func setting(_ name: String) throws -> String {
        guard let value = ProcessInfo.processInfo.environment[name], !value.isEmpty else {
            throw XCTSkip("Run scripts/verify-ios-acceptance.py to supply the isolated acceptance environment.")
        }
        return value
    }

    func testPairRegisterSyncPreferencesAndRevokeAgainstGo() async throws {
        let base = try setting("QUAKERELAY_ACCEPTANCE_BASE_URL")
        let parts = try XCTUnwrap(URLComponents(string: base))
        XCTAssertEqual(parts.scheme, "http")
        guard parts.host == "localhost", parts.port != nil else {
            XCTFail("Mutating acceptance requests are restricted to the local fixture server.")
            return
        }
        let suite = "acceptance.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let settings = AppSettings(defaults: defaults)
        settings.serverBaseURL = base
        let credentials = KeychainCredentialStore(service: suite)
        defer { try? credentials.clearAccessToken() }
        let api = APIClient(settings: settings, credentials: credentials)
        let health = try await api.health()
        XCTAssertTrue(health.ok && health.db && health.migrationCurrent)
        XCTAssertFalse(health.apnsHttp2, "The fixture relay must remain offline.")

        let installation = try credentials.installationID()
        let paired = try await api.completePairing(
            code: try setting("QUAKERELAY_ACCEPTANCE_PAIRING_CODE"),
            installationID: installation, serverBaseURL: base
        )
        XCTAssertTrue(paired.ok)
        XCTAssertFalse(paired.deviceAccessToken.isEmpty)
        try credentials.saveAccessToken(paired.deviceAccessToken, for: base)
        var preferences = DevicePreferences(
            notificationsEnabled: true, timeSensitiveEnabled: true,
            customSoundEnabled: true, eventTypes: ["eew_forecast", "eew_cancel"]
        )
        let registered = try await api.registerDevice(DeviceRegistrationRequest(
            installationId: installation, deviceToken: String(repeating: "ab", count: 32),
            environment: "development", appVersion: "acceptance", osVersion: "simulator",
            deviceName: "Disposable simulator fixture", preferences: preferences
        ))
        XCTAssertEqual(registered.device.installationId, installation)
        XCTAssertTrue(registered.device.active)
        XCTAssertEqual(registered.device.environment, "development")

        let container = try PersistenceController.makeContainer(inMemory: true)
        let context = container.mainContext
        try SyncPageApplier.bindServer(try api.serverIdentity(), in: context)
        let first = try await api.sync(after: 0, limit: 2)
        XCTAssertTrue(first.hasMore)
        XCTAssertEqual(first.items.count, 2)
        try SyncPageApplier.apply(first, expectedAfter: 0, to: context)
        let second = try await api.sync(after: first.nextAfterSequence, limit: 2)
        XCTAssertTrue(second.hasMore)
        XCTAssertEqual(second.items.count, 2)
        try SyncPageApplier.apply(second, expectedAfter: first.nextAfterSequence, to: context)
        XCTAssertEqual(second.items.last?.event.sourceSerial, 3)
        XCTAssertEqual(second.items.last?.event.isCancelled, true)
        let third = try await api.sync(after: second.nextAfterSequence, limit: 2)
        XCTAssertTrue(third.hasMore)
        XCTAssertEqual(third.items.count, 2)
        try SyncPageApplier.apply(third, expectedAfter: second.nextAfterSequence, to: context)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 6)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<EventEntity>()), 2)
        XCTAssertEqual(try SyncPageApplier.fetchOrCreateCursor(in: context).lastServerSequence, 6)
        let forecast = try XCTUnwrap(third.items.first?.report)
        let warning = try XCTUnwrap(third.items.last?.report)
        XCTAssertEqual(forecast.telegramType, "VXSE45")
        XCTAssertEqual(forecast.hypocenter?.status, "assumed")
        XCTAssertEqual(forecast.hypocenter?.magnitude, 1)
        XCTAssertEqual(forecast.hypocenter?.depthKm, 10)
        XCTAssertNil(forecast.magnitude, "Legacy fields must not expose unqualified assumed values.")
        XCTAssertEqual(warning.telegramType, "VXSE43")
        XCTAssertNil(warning.hypocenter?.magnitude)
        XCTAssertNil(warning.hypocenter?.depthKm)
        XCTAssertEqual(warning.maxIntensity, "5弱以上")
        XCTAssertTrue(warning.body.contains("合成震源") && warning.body.contains("仮定") && warning.body.contains("予想最大震度5弱以上"))
        let storedReports = try context.fetch(FetchDescriptor<ReportEntity>())
        let reference = try XCTUnwrap(ForecastReference.latest(eventID: warning.eventId, reports: storedReports))
        XCTAssertEqual(reference.id, forecast.id)
        XCTAssertTrue(reference.numericHypocenter?.isAssumed == true)
        XCTAssertEqual(reference.numericHypocenter?.latitude, 31.8)
        let events = try await api.events()
        let event = try XCTUnwrap(events.items.first { $0.id == first.items.first?.event.id })
        let detail = try await api.event(id: event.id)
        XCTAssertEqual(detail.reports.count, 4)
        XCTAssertTrue(detail.event.isCancelled)
        let tail = try await api.sync(after: third.nextAfterSequence)
        XCTAssertEqual(tail.items.count, 4)
        XCTAssertEqual(tail.nextAfterSequence, 10)
        try SyncPageApplier.apply(tail, expectedAfter: third.nextAfterSequence, to: context)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<ReportEntity>()), 10)
        XCTAssertEqual(try context.fetchCount(FetchDescriptor<EventEntity>()), 6)
        let ordinary = try XCTUnwrap(tail.items.first?.report)
        XCTAssertEqual(ordinary.revision, 9)
        XCTAssertFalse(ordinary.isFinal)
        let normalStored = ReportEntity(dto: ordinary, serverSequence: Int64(ordinary.serverSequence))
        XCTAssertEqual(normalStored.publicationLabel, "発表")
        let tsunami = try XCTUnwrap(tail.items.first { $0.event.category == "tsunami" && $0.report.telegramType == "VTSE41" })
        XCTAssertTrue(tsunami.event.id.hasPrefix("tsunami-"))
        XCTAssertEqual(tsunami.report.eventType, "tsunami_warning")
        let stored = ReportEntity(dto: tsunami.report, serverSequence: Int64(tsunami.report.serverSequence))
        XCTAssertTrue(stored.bulletin?.sections?.contains(where: { $0.rows?.contains(where: { $0.value == "巨大" }) == true }) == true)
        let source = try await api.sourceDocument(reportID: tsunami.report.id)
        XCTAssertTrue(String(decoding: source, as: UTF8.self).contains("合成予報区"))
        XCTAssertTrue(tail.items.contains { $0.event.category == "advisory" && $0.report.eventType == "nankai_info" })
        XCTAssertTrue(tail.items.contains { $0.report.telegramType == "WEPA60" && $0.report.bulletin?.document?.format == "a/n" })
        preferences.eventTypes += ["tsunami_warning", "tsunami_info", "nankai_info", "seismic_advisory", "earthquake_data"]

        let status = try await api.receiverStatus()
        XCTAssertEqual(status.sourceConfigured, false)
        XCTAssertEqual(status.sourceFresh, false)
        XCTAssertEqual(status.db, true)
        preferences.earthquakeRegions = ["宮崎県"]
        preferences.tsunamiRegions = ["宮崎県"]
        preferences.minimumIntensity = "4"
        preferences.liveActivitiesEnabled = false
        preferences.notificationsEnabled = false
        let changed = try await api.updatePreferences(preferences)
        XCTAssertFalse(changed.preferences.notificationsEnabled)
        XCTAssertEqual(changed.preferences.earthquakeRegions, ["宮崎県"])
        XCTAssertEqual(changed.preferences.tsunamiRegions, ["宮崎県"])
        XCTAssertEqual(changed.preferences.minimumIntensity, "4")
        XCTAssertEqual(changed.preferences.liveActivitiesEnabled, false)
        // Synthetic ActivityKit token registration exercises the authenticated
        // wire shape; the offline fixture cannot send any Apple pushes.
        try await api.registerLiveActivityStartToken("aabb")
        try await api.clearLiveActivityStartToken()
        let current = try await api.currentDevice()
        XCTAssertFalse(current.device.preferences.notificationsEnabled)
        try await api.revokeDevice()
        do {
            _ = try await api.sync(after: 0)
            XCTFail("A revoked credential must be rejected by the Go server.")
        } catch let APIClientError.server(status, _, _, _) {
            XCTAssertEqual(status, 401)
        }
    }

    func testDeployedVPSReadOnlyFromSimulator() async throws {
        let base = try setting("QUAKERELAY_ACCEPTANCE_LIVE_URL")
        guard let endpoint = URLComponents(string: base), endpoint.scheme == "https",
              endpoint.host != nil, endpoint.user == nil, endpoint.password == nil,
              endpoint.path == "/api/v1", endpoint.query == nil, endpoint.fragment == nil else {
            XCTFail("Unexpected public acceptance endpoint.")
            return
        }
        var origin = endpoint
        origin.path = ""
        let originURL = try XCTUnwrap(origin.url)
        let suite = "public-acceptance.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let settings = AppSettings(defaults: defaults)
        settings.serverBaseURL = base
        let api = APIClient(settings: settings, credentials: KeychainCredentialStore(service: suite))
        let health = try await api.health()
        XCTAssertTrue(health.ok && health.db && health.configLoaded && health.migrationCurrent)
        XCTAssertTrue(health.apnsHttp2, "This field means configured, not Apple acceptance.")
        let session = URLSession(configuration: .ephemeral)
        defer { session.invalidateAndCancel() }
        for (path, expected) in [("/readyz", 200), ("/api/v1/events", 401), ("/metrics", 401)] {
            let url = try XCTUnwrap(URL(string: path, relativeTo: originURL)?.absoluteURL)
            let (body, response) = try await session.data(from: url)
            let http = try XCTUnwrap(response as? HTTPURLResponse)
            XCTAssertEqual(http.statusCode, expected)
            XCTAssertEqual(http.value(forHTTPHeaderField: "Cache-Control"), "no-store")
            if path == "/readyz" {
                let state = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Bool])
                for key in ["ok", "db", "source_connected", "apns_configured"] {
                    XCTAssertEqual(state[key], true)
                }
            }
        }
    }
}
