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
    let historyPreferences = HistoryPreferences()
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
    let liveActivities: LiveActivityCoordinator

    @Published private(set) var receiverStatus: ReceiverStatusResponse?
    @Published private(set) var receiverStatusCheckedAt: Date?
    @Published private(set) var receiverStatusError: String?
    @Published private(set) var isTestingNotification = false
    @Published private(set) var notificationTestResult: NotificationTestDTO?
    @Published private(set) var notificationTestError: String?
    @Published private(set) var notificationTestAvailableAt: Date?
    private var testRequestID: String?
    private var testRequestStyle: String?

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
        self.liveActivities = LiveActivityCoordinator(api: resolvedAPI)
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

        // Resolve existing settings and the one-time category expansion before APNs registration.
        if isPaired { await refreshRemotePreferences() }
        await notifications.configureAndRegisterForRemoteNotifications()
        if isPaired { _ = await repository.syncAll(); await refreshReceiverStatus() }
        await liveActivities.configure(enabled: settings.notificationsEnabled && settings.liveActivitiesEnabled, paired: isPaired)
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
            receiverStatus = nil; receiverStatusCheckedAt = nil; receiverStatusError = nil
            await liveActivities.configure(enabled: settings.notificationsEnabled && settings.liveActivitiesEnabled, paired: true)
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
            receiverStatus = nil; receiverStatusCheckedAt = nil; receiverStatusError = nil
            notificationTestResult = nil; notificationTestError = nil; notificationTestAvailableAt = nil
            testRequestID = nil
            await liveActivities.configure(enabled: false, paired: false)
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
            settings.expandedPreferencesSaved()
            await liveActivities.configure(enabled: settings.notificationsEnabled && settings.liveActivitiesEnabled, paired: true)
            return true
        } catch {
            diagnosticsError = error.localizedDescription
            return false
        }
    }

    func monitorReceiverStatus() async {
        while !Task.isCancelled {
            if isPaired { await refreshReceiverStatus() }
            do { try await Task.sleep(for: .seconds(30)) }
            catch { return }
        }
    }

    func refreshReceiverStatus() async {
        guard isPaired else { return }
        do {
            receiverStatus = try await api.receiverStatus()
            receiverStatusCheckedAt = Date()
            receiverStatusError = nil
        } catch {
            if !Task.isCancelled { receiverStatusError = error.localizedDescription }
        }
    }

    func sendNotificationTest(style: String) async {
        guard isPaired, !isTestingNotification else { return }
        isTestingNotification = true
        notificationTestError = nil
        defer { isTestingNotification = false }
        if testRequestID == nil || testRequestStyle != style {
            testRequestID = UUID().uuidString
            testRequestStyle = style
        }
        do {
            let response = try await api.requestNotificationTest(id: testRequestID!, style: style)
            notificationTestResult = response.test
            let requested = ServerDateParser.parse(response.test.requestedAt) ?? Date()
            notificationTestAvailableAt = requested.addingTimeInterval(TimeInterval(response.cooldownSeconds))
            testRequestID = nil
        } catch {
            notificationTestError = error.localizedDescription
            // Retain the request ID after an uncertain transport result.
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
            if settings.needsExpandedPreferences {
                _ = try await api.updatePreferences(settings.devicePreferences)
                settings.expandedPreferencesSaved()
            }
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
            await refreshReceiverStatus()
            await liveActivities.configure(enabled: settings.notificationsEnabled && settings.liveActivitiesEnabled, paired: true)
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
