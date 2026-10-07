import Foundation
import XCTest
@testable import QuakeRelay

@MainActor
final class DeviceRegistrationCoordinatorTests: XCTestCase {
    func testPairingPreferencesRemainPendingAfterFailureAndResetOnlyAfterSuccess() async throws {
        let api = FakeDeviceRegistrationService()
        api.failuresRemaining = 1
        let credentials = FakeDeviceCredentials(accessToken: nil)
        let settings = makeSettings()
        let coordinator = DeviceRegistrationCoordinator(
            api: api,
            credentials: credentials,
            settings: settings
        )

        coordinator.receivedAPNsToken(token(byte: 0x11))
        await waitUntil { coordinator.state == .awaitingPairing }

        credentials.storedAccessToken = "paired-device-token"
        coordinator.pairingCompleted()
        await waitUntil {
            guard api.requests.count == 1 else { return false }
            if case .failed = coordinator.state { return true }
            return false
        }

        XCTAssertNotNil(api.requests[0].preferences)

        await coordinator.registerCurrentTokenIfPossible()
        XCTAssertEqual(api.requests.count, 2)
        XCTAssertNotNil(api.requests[1].preferences, "A failed registration must retain pending pairing preferences")

        await coordinator.registerCurrentTokenIfPossible()
        XCTAssertEqual(api.requests.count, 3)
        XCTAssertNil(api.requests[2].preferences, "A successful preference registration must make later launch registration non-destructive")
    }

    func testTokenReplacementDuringRegistrationQueuesLatestToken() async {
        let api = FakeDeviceRegistrationService()
        api.suspendNextRegistration = true
        let credentials = FakeDeviceCredentials(accessToken: "paired-device-token")
        let coordinator = DeviceRegistrationCoordinator(
            api: api,
            credentials: credentials,
            settings: makeSettings()
        )

        coordinator.receivedAPNsToken(token(byte: 0x22))
        await waitUntil { api.requests.count == 1 && api.hasSuspendedRegistration }

        coordinator.receivedAPNsToken(token(byte: 0x33))
        await Task.yield()
        api.resumeSuspendedRegistration()

        await waitUntil { api.requests.count == 2 }
        XCTAssertEqual(api.requests[0].deviceToken, String(repeating: "22", count: 32))
        XCTAssertEqual(api.requests[1].deviceToken, String(repeating: "33", count: 32))
        XCTAssertNil(api.requests[0].preferences)
        XCTAssertNil(api.requests[1].preferences)
    }

    func testPairingDuringNormalRegistrationQueuesPreferenceBearingFollowup() async {
        let api = FakeDeviceRegistrationService()
        api.suspendNextRegistration = true
        let credentials = FakeDeviceCredentials(accessToken: "paired-device-token")
        let coordinator = DeviceRegistrationCoordinator(
            api: api,
            credentials: credentials,
            settings: makeSettings()
        )

        coordinator.receivedAPNsToken(token(byte: 0x44))
        await waitUntil { api.requests.count == 1 && api.hasSuspendedRegistration }
        XCTAssertNil(api.requests[0].preferences)

        coordinator.pairingCompleted()
        await Task.yield()
        api.resumeSuspendedRegistration()

        await waitUntil { api.requests.count == 2 }
        XCTAssertNotNil(api.requests[1].preferences)
    }

    private func makeSettings() -> AppSettings {
        let suite = "jp.kb-dev.quickrelay.registration-tests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return AppSettings(defaults: defaults)
    }

    private func token(byte: UInt8) -> Data {
        Data(repeating: byte, count: 32)
    }

    private func waitUntil(
        _ condition: @escaping @MainActor () -> Bool,
        file: StaticString = #filePath,
        line: UInt = #line
    ) async {
        for _ in 0..<1_000 {
            if condition() { return }
            await Task.yield()
        }
        XCTFail("Timed out waiting for asynchronous registration state", file: file, line: line)
    }
}

private enum FakeRegistrationError: LocalizedError {
    case rejected

    var errorDescription: String? { "fake registration failure" }
}

@MainActor
private final class FakeDeviceRegistrationService: DeviceRegistrationServing {
    var requests: [DeviceRegistrationRequest] = []
    var failuresRemaining = 0
    var suspendNextRegistration = false
    private var suspendedContinuation: CheckedContinuation<DeviceResponse, Error>?

    var hasSuspendedRegistration: Bool { suspendedContinuation != nil }

    func registerDevice(_ registration: DeviceRegistrationRequest) async throws -> DeviceResponse {
        requests.append(registration)
        if suspendNextRegistration {
            suspendNextRegistration = false
            return try await withCheckedThrowingContinuation { continuation in
                suspendedContinuation = continuation
            }
        }
        if failuresRemaining > 0 {
            failuresRemaining -= 1
            throw FakeRegistrationError.rejected
        }
        return response(for: registration)
    }

    func resumeSuspendedRegistration() {
        guard let continuation = suspendedContinuation, let registration = requests.last else {
            XCTFail("No suspended registration to resume")
            return
        }
        suspendedContinuation = nil
        continuation.resume(returning: response(for: registration))
    }

    private func response(for registration: DeviceRegistrationRequest) -> DeviceResponse {
        let preferences = registration.preferences ?? DevicePreferences(
            notificationsEnabled: true,
            timeSensitiveEnabled: true,
            customSoundEnabled: true,
            eventTypes: RelayEventType.allCases.map(\.rawValue)
        )
        return DeviceResponse(
            ok: true,
            device: DeviceDTO(
                installationId: registration.installationId,
                deviceName: registration.deviceName,
                environment: registration.environment,
                appVersion: registration.appVersion,
                osVersion: registration.osVersion,
                active: true,
                preferences: preferences,
                lastSeenAt: "2026-08-13T13:31:00.000Z"
            )
        )
    }
}

private final class FakeDeviceCredentials: DeviceCredentialStoring {
    var storedAccessToken: String?
    private let identifier = "1777a91b-13c5-4c8c-9374-1888ead66fac"

    init(accessToken: String?) {
        storedAccessToken = accessToken
    }

    func installationID() throws -> String { identifier }
    func accessToken(for _: String) throws -> String? { storedAccessToken }
    func saveAccessToken(_ token: String, for _: String) throws { storedAccessToken = token }
    func clearAccessToken() throws { storedAccessToken = nil }
}
