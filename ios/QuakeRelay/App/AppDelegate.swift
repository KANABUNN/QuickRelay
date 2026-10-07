import UIKit

@MainActor
final class AppDelegate: NSObject, UIApplicationDelegate {
    weak var environment: AppEnvironment?

    func bind(environment: AppEnvironment) {
        self.environment = environment
    }

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        environment?.deviceRegistration.receivedAPNsToken(deviceToken)
    }

    func application(
        _ application: UIApplication,
        didFailToRegisterForRemoteNotificationsWithError error: Error
    ) {
        environment?.deviceRegistration.registrationFailed(error)
    }

    func application(
        _ application: UIApplication,
        didReceiveRemoteNotification userInfo: [AnyHashable: Any],
        fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void
    ) {
        Task { @MainActor [weak self] in
            guard let environment = self?.environment else {
                completionHandler(.noData)
                return
            }
            let outcome = await environment.handleRemoteNotification(userInfo)
            switch outcome {
            case .newData: completionHandler(.newData)
            case .noData: completionHandler(.noData)
            case .failed: completionHandler(.failed)
            }
        }
    }
}
