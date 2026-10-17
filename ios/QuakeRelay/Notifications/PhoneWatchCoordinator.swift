import Combine
import Foundation
import WatchConnectivity

@MainActor
final class PhoneWatchCoordinator: NSObject, ObservableObject {
    @Published private(set) var lastSentAt: Date?
    @Published private(set) var lastError: String?
    var makeSnapshot: (() -> WatchSnapshot?)?
    var onSyncRequested: (() async -> Void)?
    private var started = false

    func start() {
        guard !started, WCSession.isSupported() else { return }
        started = true
        let session = WCSession.default
        session.delegate = self
        session.activate()
    }

    func publish() {
        guard started, WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated, session.isPaired, session.isWatchAppInstalled else { return }
        guard let snapshot = makeSnapshot?() else { return }
        do {
            try session.updateApplicationContext([WatchSnapshot.applicationContextKey: try snapshot.encoded()])
            lastSentAt = snapshot.generatedAt
            lastError = nil
        } catch {
            lastError = "Apple Watchへ情報を同期できません。接続を確認してください。"
        }
    }
}

extension PhoneWatchCoordinator: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        Task { @MainActor [weak self] in
            if error != nil { self?.lastError = "Apple Watchとの接続を開始できません。" }
            else if activationState == .activated { self?.publish() }
        }
    }
    nonisolated func sessionDidBecomeInactive(_ session: WCSession) {}
    nonisolated func sessionDidDeactivate(_ session: WCSession) { session.activate() }
    nonisolated func sessionWatchStateDidChange(_ session: WCSession) {
        Task { @MainActor [weak self] in self?.publish() }
    }
    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        Task { @MainActor [weak self] in self?.publish() }
    }
    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        guard message[WatchSnapshot.requestMessageKey] as? Bool == true else { replyHandler([:]); return }
        Task { @MainActor [weak self] in
            guard let self else { replyHandler([:]); return }
            await self.onSyncRequested?()
            self.publish()
            guard let snapshot = self.makeSnapshot?(), let data = try? snapshot.encoded() else {
                replyHandler([:]); return
            }
            replyHandler([WatchSnapshot.applicationContextKey: data])
        }
    }
}
