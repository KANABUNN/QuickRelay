import ActivityKit
import Combine
import Foundation

@MainActor
final class LiveActivityCoordinator: ObservableObject {
    @Published private(set) var statusText = "オフ"
    @Published private(set) var registrationError: String?
    private let registrar: LiveActivityPushTokenRegistrar
    private var enabled = false
    private var paired = false
    private var origin: String?
    private var activityObserver: Task<Void, Never>?
    private var startObserver: Task<Void, Never>?
    private var tokenObservers: [String: Task<Void, Never>] = [:]
    private var stateObservers: [String: Task<Void, Never>] = [:]
    private var uploads: [String: Task<Void, Never>] = [:]

    init(api: APIClient) { registrar = LiveActivityPushTokenRegistrar(api: api) }

    var supportsRemoteStart: Bool {
        if #available(iOS 17.2, *) { return true }
        return false
    }

    func configure(enabled: Bool, paired: Bool) async {
        self.enabled = enabled
        self.paired = paired
        origin = try? registrar.api.serverIdentity()
        registrationError = nil
        guard paired else {
            await endAllLocally()
            stopObservers()
            statusText = "ペアリング後に利用できます"
            return
        }
        if !enabled || !ActivityAuthorizationInfo().areActivitiesEnabled || !supportsRemoteStart {
            uploads.removeValue(forKey: "start")?.cancel()
        }
        if !enabled {
            await endAllLocally()
            statusText = "オフ"
        } else if !ActivityAuthorizationInfo().areActivitiesEnabled {
            statusText = "iOSの設定でLive Activityが無効です"
        } else if !supportsRemoteStart {
            statusText = "自動開始にはiOS 17.2以降が必要です"
        } else {
            statusText = "開始を待機"
        }
        if enabled && (!ActivityAuthorizationInfo().areActivitiesEnabled || !supportsRemoteStart) {
            await endAllLocally()
            do { try await registrar.api.clearLiveActivityStartToken() }
            catch { registrationError = "Live Activityの停止設定をサーバーに確認できませんでした。" }
        }
        startObservers()
        // Also retry current tokens on launch/foreground/save. Rotation observers
        // remain active while the app is backgrounded, including remote starts.
        for activity in Activity<QuickRelayActivityAttributes>.activities { observe(activity) }
        if #available(iOS 17.2, *), enabled,
           ActivityAuthorizationInfo().areActivitiesEnabled,
           let token = Activity<QuickRelayActivityAttributes>.pushToStartToken {
            uploadStart(token)
        }
    }

    private func startObservers() {
        if activityObserver == nil {
            activityObserver = Task { [weak self] in
                for await activity in Activity<QuickRelayActivityAttributes>.activityUpdates {
                    guard let self, !Task.isCancelled else { return }
                    self.observe(activity)
                }
            }
        }
        if #available(iOS 17.2, *), startObserver == nil {
            startObserver = Task { [weak self] in
                for await token in Activity<QuickRelayActivityAttributes>.pushToStartTokenUpdates {
                    guard let self, !Task.isCancelled else { return }
                    self.uploadStart(token)
                }
            }
        }
    }
    private func uploadStart(_ token: Data) {
        guard paired, enabled, ActivityAuthorizationInfo().areActivitiesEnabled else { return }
        queueUpload(key: "start") { [registrar] in try await registrar.registerStart(pushToken: token) }
    }
    private func observe(_ activity: Activity<QuickRelayActivityAttributes>) {
        guard paired else { return }
        if !enabled {
            Task { await activity.end(nil, dismissalPolicy: .immediate) }
            return
        }
        if let token = activity.pushToken { upload(token, for: activity) }
        if tokenObservers[activity.id] == nil {
            tokenObservers[activity.id] = Task { [weak self] in
                for await token in activity.pushTokenUpdates {
                    guard let self, !Task.isCancelled else { return }
                    self.upload(token, for: activity)
                }
            }
        }
        if stateObservers[activity.id] == nil {
            stateObservers[activity.id] = Task { [weak self] in
                for await state in activity.activityStateUpdates {
                    guard let self, !Task.isCancelled else { return }
                    if state == .dismissed || state == .ended {
                        self.queueUpload(key: "end:\(activity.id)") { [registrar = self.registrar] in
                            try await registrar.ended(attributes: activity.attributes, activityID: activity.id,dismissed: state == .dismissed)
                        }
                        self.tokenObservers.removeValue(forKey: activity.id)?.cancel()
                        self.uploads.removeValue(forKey: activity.id)?.cancel()
                        self.stateObservers.removeValue(forKey: activity.id)
                        return
                    }
                }
            }
        }
    }
    private func upload(_ token: Data, for activity: Activity<QuickRelayActivityAttributes>) {
        guard paired, activity.activityState != .dismissed, activity.activityState != .ended else { return }
        queueUpload(key: activity.id) { [registrar] in
            try await registrar.register(pushToken: token, attributes: activity.attributes, activityID: activity.id)
        }
    }
    private func queueUpload(key: String, operation: @escaping @MainActor () async throws -> Void) {
        let capturedOrigin = origin
        uploads[key]?.cancel()
        uploads[key] = Task { [weak self] in
            // Bounded retries fit normal foreground/background execution; retry
            // again on the next foreground transition if the network stays down.
            for attempt in 0..<6 {
                guard let self, self.paired, self.origin == capturedOrigin,
                      (try? self.registrar.api.serverIdentity()) == capturedOrigin,
                      !Task.isCancelled else { return }
                do {
                    try await operation()
                    guard !Task.isCancelled else { return }
                    self.registrationError = nil
                    if key == "start", self.enabled { self.statusText = "自動開始の登録済み" }
                    self.uploads.removeValue(forKey: key)
                    return
                } catch {
                    if Task.isCancelled { return }
                    self.registrationError = "Live Activityの登録を確認できません。アプリを開き直すと再試行します。"
                    do { try await Task.sleep(for: .seconds(1 << attempt)) }
                    catch { return }
                }
            }
        }
    }
    func endAllLocally() async {
        for activity in Activity<QuickRelayActivityAttributes>.activities {
            await activity.end(nil, dismissalPolicy: .immediate)
        }
    }
    private func stopObservers() {
        activityObserver?.cancel(); activityObserver = nil
        startObserver?.cancel(); startObserver = nil
        for task in tokenObservers.values { task.cancel() }; tokenObservers.removeAll()
        for task in stateObservers.values { task.cancel() }; stateObservers.removeAll()
        for task in uploads.values { task.cancel() }; uploads.removeAll()
    }
}
