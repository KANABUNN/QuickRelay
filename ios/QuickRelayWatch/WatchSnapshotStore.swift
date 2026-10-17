import Combine
import Foundation
import WatchConnectivity

/// Receives public event summaries from the paired iPhone. API credentials never enter this app.
@MainActor
final class WatchSnapshotStore: NSObject, ObservableObject {
    @Published private(set) var snapshot: WatchSnapshot?
    @Published private(set) var isRequesting = false
    @Published private(set) var message: String?

    private static let cacheKey = "quickRelay.watchSnapshot.v1"
    private var activated = false
    private var pendingRequest: UUID?

    override init() {
        super.init()
        if let data = UserDefaults.standard.data(forKey: Self.cacheKey) {
            snapshot = try? WatchSnapshot.decode(data)
        }
    }

    func activate() {
        guard !activated else { return }
        guard WCSession.isSupported() else {
            message = "この端末ではiPhoneとの同期を利用できません。"
            return
        }
        activated = true
        let session = WCSession.default
        session.delegate = self
        session.activate()
    }

    func requestLatest() {
        guard !isRequesting else { return }
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated else {
            activate()
            return
        }
        receive(session.receivedApplicationContext[WatchSnapshot.applicationContextKey] as? Data)
        guard session.isReachable else {
            message = "iPhoneに接続できません。iPhoneでQuick Relayを開いてから再試行してください。"
            return
        }

        let request = UUID()
        pendingRequest = request
        isRequesting = true
        message = nil
        session.sendMessage([WatchSnapshot.requestMessageKey: true], replyHandler: { [weak self] reply in
            let data = reply[WatchSnapshot.applicationContextKey] as? Data
            Task { @MainActor in
                guard let self else { return }
                // A context update can arrive before this reply. Still accept a
                // newer reply; receive() rejects snapshots that are out of order.
                if let data {
                    self.receive(data)
                } else if self.pendingRequest == request {
                    self.finishRequest()
                    self.message = "iPhoneで情報を更新しています。同期結果をお待ちください。"
                }
            }
        }, errorHandler: { [weak self] _ in
            Task { @MainActor in
                guard let self, self.pendingRequest == request else { return }
                self.finishRequest()
                self.message = "iPhoneとの同期に失敗しました。接続を確認して再試行してください。"
            }
        })

        Task { [weak self] in
            try? await Task.sleep(for: .seconds(15))
            guard let self, self.pendingRequest == request else { return }
            self.finishRequest()
            self.message = "同期結果を確認できません。iPhoneでQuick Relayを開いて再試行してください。"
        }
    }

    private func finishRequest() {
        pendingRequest = nil
        isRequesting = false
    }

    private func receive(_ data: Data?) {
        guard let data else { return }
        do {
            let incoming = try WatchSnapshot.decode(data)
            if let current = snapshot, incoming.generatedAt < current.generatedAt { return }
            snapshot = incoming
            UserDefaults.standard.set(data, forKey: Self.cacheKey)
            finishRequest()
            message = incoming.isPaired ? nil : "iPhoneでQuick Relayをサーバーに接続してください。"
        } catch {
            message = "受信した情報を読み込めません。iPhoneとWatchのアプリを更新してください。"
        }
    }
}

extension WatchSnapshotStore: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        let data = session.receivedApplicationContext[WatchSnapshot.applicationContextKey] as? Data
        Task { @MainActor [weak self] in
            guard let self else { return }
            if error != nil {
                self.message = "iPhoneとの接続を開始できません。アプリを開き直してください。"
                return
            }
            self.receive(data)
            if activationState == .activated { self.requestLatest() }
        }
    }

    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        guard session.isReachable else { return }
        Task { @MainActor [weak self] in self?.requestLatest() }
    }

    nonisolated func session(_ session: WCSession, didReceiveApplicationContext applicationContext: [String: Any]) {
        let data = applicationContext[WatchSnapshot.applicationContextKey] as? Data
        Task { @MainActor [weak self] in self?.receive(data) }
    }

    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any]) {
        let data = message[WatchSnapshot.applicationContextKey] as? Data
        Task { @MainActor [weak self] in self?.receive(data) }
    }

    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        let data = message[WatchSnapshot.applicationContextKey] as? Data
        Task { @MainActor [weak self] in self?.receive(data) }
        replyHandler([:])
    }
}
