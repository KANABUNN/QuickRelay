import Combine
import Foundation
import UIKit

@MainActor
protocol DeviceRegistrationServing: AnyObject {
    func registerDevice(_ registration: DeviceRegistrationRequest) async throws -> DeviceResponse
}

extension APIClient: DeviceRegistrationServing {}

@MainActor
final class DeviceRegistrationCoordinator: ObservableObject {
    enum State: Equatable {
        case awaitingAPNsToken
        case awaitingPairing
        case registering
        case registered(Date)
        case failed(String)

        var displayText: String {
            switch self {
            case .awaitingAPNsToken: "APNs token待機中"
            case .awaitingPairing: "ペアリング待機中"
            case .registering: "サーバー登録中"
            case let .registered(date): "登録済み（\(date.formatted(date: .abbreviated, time: .standard))）"
            case let .failed(message): "失敗: \(message)"
            }
        }
    }

    @Published private(set) var state: State = .awaitingAPNsToken

    private let api: any DeviceRegistrationServing
    private let credentials: DeviceCredentialStoring
    private let settings: AppSettings
    private var inMemoryAPNsToken: String?
    private var preferencesRequestGeneration: UInt64 = 0
    private var completedPreferencesGeneration: UInt64 = 0
    private var registrationInFlight = false
    private var registrationRequestedWhileInFlight = false

    init(
        api: any DeviceRegistrationServing,
        credentials: DeviceCredentialStoring,
        settings: AppSettings
    ) {
        self.api = api
        self.credentials = credentials
        self.settings = settings
    }

    func receivedAPNsToken(_ data: Data) {
        inMemoryAPNsToken = data.map { String(format: "%02x", $0) }.joined()
        Task { await registerCurrentTokenIfPossible() }
    }

    func registrationFailed(_ error: Error) {
        state = .failed(error.localizedDescription)
    }

    func pairingCompleted() {
        preferencesRequestGeneration &+= 1
        Task { await registerCurrentTokenIfPossible() }
    }

    func registerCurrentTokenIfPossible() async {
        guard !registrationInFlight else {
            registrationRequestedWhileInFlight = true
            return
        }
        guard let token = inMemoryAPNsToken else {
            state = .awaitingAPNsToken
            return
        }
        guard (try? credentials.accessToken(for: settings.serverBaseURL)) != nil else {
            state = .awaitingPairing
            return
        }

        let preferencesGeneration = preferencesRequestGeneration
        let shouldSendPreferences = preferencesGeneration > completedPreferencesGeneration
        registrationInFlight = true
        state = .registering
        defer {
            registrationInFlight = false
            let shouldRegisterAgain =
                registrationRequestedWhileInFlight ||
                inMemoryAPNsToken != token ||
                preferencesRequestGeneration > preferencesGeneration
            registrationRequestedWhileInFlight = false
            if shouldRegisterAgain {
                Task { await registerCurrentTokenIfPossible() }
            }
        }
        do {
            guard ["development", "production"].contains(Self.apnsEnvironment) else {
                throw APIClientError.invalidResponse
            }
            let installationID = try credentials.installationID()
            let registration = DeviceRegistrationRequest(
                installationId: installationID,
                deviceToken: token,
                environment: Self.apnsEnvironment,
                appVersion: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0",
                osVersion: UIDevice.current.systemVersion,
                deviceName: UIDevice.current.name,
                preferences: shouldSendPreferences ? settings.devicePreferences : nil
            )
            _ = try await api.registerDevice(registration)
            if shouldSendPreferences {
                completedPreferencesGeneration = max(
                    completedPreferencesGeneration,
                    preferencesGeneration
                )
            }
            state = .registered(Date())
        } catch {
            state = .failed(error.localizedDescription)
        }
    }

    private static var apnsEnvironment: String {
        // The Info.plist value uses the same build setting as the entitlement.
        // This also supports distribution configurations that are not named Release.
        Bundle.main.object(forInfoDictionaryKey: "QuakeRelayAPNsEnvironment") as? String ?? ""

    }
}
