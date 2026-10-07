import SwiftUI
import UIKit

struct DiagnosticsView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var repository: EventRepository
    @EnvironmentObject private var notifications: NotificationCoordinator
    @EnvironmentObject private var deviceRegistration: DeviceRegistrationCoordinator

    @State private var installationID = ""

    var body: some View {
        List {
            Section("API") {
                LabeledContent("Health", value: healthText)
                if let health = environment.health {
                    LabeledContent("Server version", value: health.version)
                    LabeledContent("Configuration", value: health.configLoaded ? "OK" : "NG")
                    LabeledContent("Migration", value: health.migrationCurrent ? "Current" : "Outdated")
                    LabeledContent("Database", value: health.db ? "OK" : "NG")
                    LabeledContent("APNs設定", value: health.apnsHttp2 ? "設定済み（到達未確認）" : "未設定")
                }
                Button("API疎通を確認") {
                    Task { await environment.checkHealth() }
                }
            }

            Section("同期") {
                LabeledContent("最終 server_sequence", value: String(repository.lastServerSequence))
                LabeledContent("サーバー最新 sequence", value: String(repository.latestCommittedSequence))
                LabeledContent("最終同期", value: repository.lastSyncAt?.formatted(date: .abbreviated, time: .standard) ?? "未実行")
                Button {
                    Task { _ = await repository.syncAll() }
                } label: {
                    if repository.isSyncing { ProgressView() } else { Text("今すぐ同期") }
                }
                .disabled(repository.isSyncing)
            }

            Section("通知") {
                LabeledContent("通知権限", value: notifications.permission.authorization.rawValue)
                LabeledContent("Time Sensitive", value: notifications.permission.timeSensitive.rawValue)
                LabeledContent("APNs登録", value: deviceRegistration.state.displayText)
                Button("権限状態を更新") {
                    Task { await notifications.refreshPermissionState() }
                }
            }

            Section("端末") {
                LabeledContent("Installation ID", value: installationID)
                    .font(.caption)
                    .textSelection(.enabled)
                LabeledContent("App version", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown")
                LabeledContent("iOS", value: UIDevice.current.systemVersion)
            }

            if let error = environment.startupError ?? environment.diagnosticsError ?? repository.lastError ?? notifications.lastRegistrationError {
                Section("直近のエラー") {
                    Text(error)
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                }
            }

            Section {
                Text("診断画面・ログにはdevice access tokenおよびAPNs tokenを表示しません。")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .navigationTitle("診断")
        .task {
            installationID = (try? environment.credentials.installationID()) ?? "取得失敗"
        }
    }

    private var healthText: String {
        guard let health = environment.health else { return "未確認" }
        return health.ok ? "OK" : "NG"
    }
}
