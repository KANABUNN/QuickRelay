import Combine
import Foundation
import SwiftData

enum BackgroundSyncOutcome {
    case newData
    case noData
    case failed
}

enum PairingSafetyError: LocalizedError, Equatable {
    case invalidServer
    case serverChangedDuringPairing

    var errorDescription: String? {
        switch self {
        case .invalidServer:
            "接続先URLが正しくありません。資格情報は保存していません。"
        case .serverChangedDuringPairing:
            "ペアリング中に接続先が変更されました。資格情報は保存していません。接続先を確認して再試行してください。"
        }
    }
}

enum PairingServerBinding {
    static func capture(serverBaseURL: String) throws -> String {
        guard let origin = ServerCredentialOrigin.normalized(from: serverBaseURL) else {
            throw PairingSafetyError.invalidServer
        }
        return origin
    }

    static func validateUnchanged(capturedOrigin: String, currentServerBaseURL: String) throws {
        guard capturedOrigin == ServerCredentialOrigin.normalized(from: currentServerBaseURL) else {
            throw PairingSafetyError.serverChangedDuringPairing
        }
    }
}

@MainActor
final class AppEnvironment: ObservableObject {
    @Published private(set) var isPaired = false
    @Published private(set) var pairingError: String?
    @Published private(set) var startupError: String?
    @Published private(set) var health: HealthResponse?
    @Published private(set) var diagnosticsError: String?
    @Published private(set) var isSavingPreferences = false

    let modelContainer: ModelContainer
    let settings: AppSettings
    let credentials: DeviceCredentialStoring
    let api: APIClient
    let repository: EventRepository
    let router: DeepLinkRouter
    let notifications: NotificationCoordinator
    let deviceRegistration: DeviceRegistrationCoordinator
    let liveActivityTokenRegistrar: any LiveActivityPushTokenRegistrar

    private var started = false
    private var pairingInProgress = false

    init(
        inMemory: Bool = false,
        settings: AppSettings? = nil,
        credentials: DeviceCredentialStoring? = nil
    ) {
        let resolvedSettings = settings ?? AppSettings()
        let resolvedCredentials = credentials ?? KeychainCredentialStore()
        let container: ModelContainer
        var containerError: String?
        do {
            container = try PersistenceController.makeContainer(inMemory: inMemory)
            containerError = nil
        } catch {
            // Never delete a possibly recoverable store automatically. An
            // in-memory fallback keeps diagnostics and pairing available.
            container = try! PersistenceController.makeContainer(inMemory: true)
            containerError = "ローカルDBを開けなかったため一時メモリで起動しました: \(error.localizedDescription)"
        }

        let resolvedRouter = DeepLinkRouter()
        let resolvedAPI = APIClient(settings: resolvedSettings, credentials: resolvedCredentials)
        let resolvedRepository = EventRepository(context: container.mainContext, api: resolvedAPI)
        let resolvedNotifications = NotificationCoordinator(settings: resolvedSettings, router: resolvedRouter)

        self.modelContainer = container
        self.settings = resolvedSettings
        self.credentials = resolvedCredentials
        self.api = resolvedAPI
        self.repository = resolvedRepository
        self.router = resolvedRouter
        self.notifications = resolvedNotifications
        self.deviceRegistration = DeviceRegistrationCoordinator(
            api: resolvedAPI,
            credentials: resolvedCredentials,
            settings: resolvedSettings
        )
        self.liveActivityTokenRegistrar = DisabledLiveActivityPushTokenRegistrar()
        self.startupError = containerError

        resolvedNotifications.onSyncRequested = { [weak resolvedRepository] in
            Task { @MainActor in
                _ = await resolvedRepository?.syncAll()
            }
        }
    }

    func start() async {
        guard !started else { return }
        started = true
        do {
            isPaired = try credentials.accessToken(for: settings.serverBaseURL) != nil
            _ = try credentials.installationID()
        } catch {
            startupError = error.localizedDescription
        }

        await notifications.configureAndRegisterForRemoteNotifications()
        if isPaired {
            await refreshRemotePreferences()
            _ = await repository.syncAll()
        }
    }

    func pair(code: String) async -> Bool {
        guard !pairingInProgress, !repository.isSyncing else {
            pairingError = "同期またはペアリングの完了後に再試行してください。"
            return false
        }
        let isASCIIDigits = code.utf8.allSatisfy { (48...57).contains($0) }
        guard code.utf8.count == 8, isASCIIDigits else {
            pairingError = "ペアリングコードは半角数字8桁です。"
            return false
        }

        pairingInProgress = true
        pairingError = nil
        defer { pairingInProgress = false }
        do {
            let pairingServerURL = settings.serverBaseURL
            let pairingOrigin = try PairingServerBinding.capture(serverBaseURL: pairingServerURL)
            let installationID = try credentials.installationID()
            let response = try await api.completePairing(
                code: code,
                installationID: installationID,
                serverBaseURL: pairingServerURL
            )
            try PairingServerBinding.validateUnchanged(
                capturedOrigin: pairingOrigin,
                currentServerBaseURL: settings.serverBaseURL
            )
            try credentials.saveAccessToken(response.deviceAccessToken, for: pairingServerURL)
            isPaired = true
            deviceRegistration.pairingCompleted()
            _ = await repository.syncAll()
            return true
        } catch {
            pairingError = error.localizedDescription
            return false
        }
    }

    func unpair() async {
        guard !repository.isSyncing, !pairingInProgress else {
            diagnosticsError = "同期またはペアリングの完了後に解除してください。"
            return
        }
        pairingInProgress = true
        defer { pairingInProgress = false }
        do {
            do {
                try await api.revokeDevice()
            } catch APIClientError.server(status: 401, code: _, message: _, requestID: _) {
                // Revoked / obsolete credentials must not trap the app in a paired state.
            }
            try credentials.clearAccessToken()
            isPaired = false
            router.reset()
            pairingError = nil
        } catch {
            pairingError = error.localizedDescription
        }
    }

    func savePreferences() async -> Bool {
        guard isPaired, !isSavingPreferences else { return false }
        isSavingPreferences = true
        diagnosticsError = nil
        defer { isSavingPreferences = false }
        do {
            _ = try await api.updatePreferences(settings.devicePreferences)
            return true
        } catch {
            diagnosticsError = error.localizedDescription
            return false
        }
    }

    func checkHealth() async {
        diagnosticsError = nil
        do {
            health = try await api.health()
        } catch {
            health = nil
            diagnosticsError = error.localizedDescription
        }
    }

    private func refreshRemotePreferences() async {
        do {
            let response = try await api.currentDevice()
            settings.apply(response.device.preferences)
        } catch {
            // Registration and history sync remain useful during a temporary
            // preferences read failure; expose it only through diagnostics.
            diagnosticsError = error.localizedDescription
        }
    }

    func becameActive() async {
        await notifications.refreshPermissionState()
        if isPaired {
            // Ask Apple again before retrying the server registration. A cached token
            // must not reactivate a token APNs has since invalidated.
            notifications.registerForRemoteNotifications()
            _ = await repository.syncAll()
        }
    }

    func handle(url: URL) {
        router.open(url)
    }

    func handleRemoteNotification(_: [AnyHashable: Any]) async -> BackgroundSyncOutcome {
        // Background receipt is a synchronization trigger only. Navigation is
        // reserved for UNUserNotificationCenterDelegate's explicit tap path.
        guard isPaired else { return .noData }
        let previousSequence = repository.lastServerSequence
        guard await repository.syncAll() else { return .failed }
        return repository.lastServerSequence > previousSequence ? .newData : .noData
    }
}
