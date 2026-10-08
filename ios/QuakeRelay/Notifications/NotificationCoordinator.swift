import Combine
import UIKit
import UserNotifications

struct NotificationPermissionSnapshot: Equatable {
    enum State: String {
        case notDetermined
        case denied
        case authorized
        case provisional
        case ephemeral
        case unknown
    }

    enum TimeSensitiveState: String {
        case notSupported
        case disabled
        case enabled
        case unknown
    }

    let authorization: State
    let timeSensitive: TimeSensitiveState

    static let unknown = NotificationPermissionSnapshot(
        authorization: .unknown,
        timeSensitive: .unknown
    )

    static func map(
        authorizationStatus: UNAuthorizationStatus,
        timeSensitiveSetting: UNNotificationSetting
    ) -> NotificationPermissionSnapshot {
        let authorization: State
        switch authorizationStatus {
        case .notDetermined: authorization = .notDetermined
        case .denied: authorization = .denied
        case .authorized: authorization = .authorized
        case .provisional: authorization = .provisional
        case .ephemeral: authorization = .ephemeral
        @unknown default: authorization = .unknown
        }

        let timeSensitive: TimeSensitiveState
        switch timeSensitiveSetting {
        case .notSupported: timeSensitive = .notSupported
        case .disabled: timeSensitive = .disabled
        case .enabled: timeSensitive = .enabled
        @unknown default: timeSensitive = .unknown
        }
        return NotificationPermissionSnapshot(
            authorization: authorization,
            timeSensitive: timeSensitive
        )
    }
}

@MainActor
final class NotificationCoordinator: NSObject, ObservableObject, UNUserNotificationCenterDelegate {
    @Published private(set) var permission = NotificationPermissionSnapshot.unknown
    @Published private(set) var lastRegistrationError: String?

    var onSyncRequested: (() -> Void)?

    private let center: UNUserNotificationCenter
    private let settings: AppSettings
    private let router: DeepLinkRouter

    init(
        settings: AppSettings,
        router: DeepLinkRouter,
        center: UNUserNotificationCenter = .current()
    ) {
        self.settings = settings
        self.router = router
        self.center = center
        super.init()
        // Install the delegate as the coordinator is constructed, before the
        // first view task. This preserves cold-launch notification responses.
        center.delegate = self
    }

    func configureAndRegisterForRemoteNotifications() async {
        do {
            _ = try await center.requestAuthorization(options: [.alert, .badge, .sound])
            lastRegistrationError = nil
        } catch {
            lastRegistrationError = error.localizedDescription
        }
        await refreshPermissionState()

        // Register on every process launch. The resulting token is never saved
        // to UserDefaults, SwiftData, files, logs, or Keychain.
        registerForRemoteNotifications()
    }

    func registerForRemoteNotifications() {
        UIApplication.shared.registerForRemoteNotifications()
    }

    func refreshPermissionState() async {
        let notificationSettings = await center.notificationSettings()
        permission = .map(
            authorizationStatus: notificationSettings.authorizationStatus,
            timeSensitiveSetting: notificationSettings.timeSensitiveSetting
        )
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        Task { @MainActor [weak self] in
            guard let self, self.settings.notificationsEnabled else {
                completionHandler([])
                return
            }
            completionHandler([.banner, .list, .sound])
            self.onSyncRequested?()
        }
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let userInfo = response.notification.request.content.userInfo
        Task { @MainActor [weak self] in
            defer { completionHandler() }
            guard let self else { return }
            if userInfo["category"] as? String == "system_test" {
                self.router.selectedTab = .settings
            } else if let route = DeepLinkParser.parse(userInfo: userInfo) {
                self.router.open(route)
            }
            self.onSyncRequested?()
        }
    }
}
