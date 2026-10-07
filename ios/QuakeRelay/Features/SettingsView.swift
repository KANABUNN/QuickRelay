import SwiftUI
import UIKit

struct SettingsView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var repository: EventRepository
    @EnvironmentObject private var notifications: NotificationCoordinator
    @EnvironmentObject private var deviceRegistration: DeviceRegistrationCoordinator

    @State private var showUnpairConfirmation = false
    @State private var saveResult: String?

    var body: some View {
        Form {
            Section("通知") {
                Toggle("通知", isOn: $settings.notificationsEnabled)
                Toggle("Time Sensitive", isOn: $settings.timeSensitiveEnabled)
                    .disabled(!settings.notificationsEnabled)
                Toggle("警報音", isOn: $settings.customSoundEnabled)
                    .disabled(!settings.notificationsEnabled)
                Toggle("通常地震通知", isOn: $settings.earthquakeNotificationsEnabled)
                    .disabled(!settings.notificationsEnabled)
                Toggle("緊急地震速報", isOn: $settings.eewNotificationsEnabled)
                    .disabled(!settings.notificationsEnabled)

                Button {
                    Task {
                        let success = await environment.savePreferences()
                        saveResult = success ? "サーバーへ保存しました。" : "保存に失敗しました。"
                    }
                } label: {
                    if environment.isSavingPreferences {
                        ProgressView()
                    } else {
                        Text("通知設定をサーバーへ保存")
                    }
                }
                .disabled(environment.isSavingPreferences)

                if let saveResult {
                    Text(saveResult)
                        .font(.caption)
                        .foregroundStyle(saveResult.contains("失敗") ? .red : .secondary)
                }
            }

            Section("接続") {
                TextField("https://quake.example.jp/api/v1", text: $settings.serverBaseURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .disabled(environment.isPaired)
                LabeledContent("ペアリング", value: environment.isPaired ? "済み" : "未設定")
                LabeledContent("APNs", value: deviceRegistration.state.displayText)
                if environment.isPaired {
                    Text("接続先を変更するには、先にこの端末のペアリングを解除してください。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            if let error = environment.pairingError ?? environment.diagnosticsError {
                Section("接続エラー") { Text(error).foregroundStyle(.red) }
            }

            Section("権限") {
                LabeledContent("通知", value: notifications.permission.authorization.rawValue)
                LabeledContent("Time Sensitive", value: notifications.permission.timeSensitive.rawValue)
                if notifications.permission.authorization == .denied {
                    Button("iOS設定を開く") {
                        guard let url = URL(string: UIApplication.openSettingsURLString) else { return }
                        UIApplication.shared.open(url)
                    }
                }
            }

            Section("同期") {
                LabeledContent("最終同期", value: repository.lastSyncAt?.formatted(date: .abbreviated, time: .standard) ?? "未実行")
                LabeledContent("最終 server_sequence", value: String(repository.lastServerSequence))
                Button {
                    Task { _ = await repository.syncAll() }
                } label: {
                    if repository.isSyncing { ProgressView() } else { Text("今すぐ同期") }
                }
                .disabled(repository.isSyncing)
            }

            Section {
                NavigationLink("診断") {
                    DiagnosticsView()
                }
            }

            Section {
                Button("この端末のペアリングを解除", role: .destructive) {
                    showUnpairConfirmation = true
                }
            } footer: {
                Text("解除するとサーバーへの端末登録を失効させ、通知を停止します。接続先を変更した後の同期で端末内の履歴を再取得します。")
            }
        }
        .navigationTitle("設定")
        .alert("ペアリングを解除しますか？", isPresented: $showUnpairConfirmation) {
            Button("解除", role: .destructive) { Task { await environment.unpair() } }
            Button("キャンセル", role: .cancel) {}
        } message: {
            Text("VPSの端末登録を失効させた後、Keychainの認証情報を削除します。通信できない場合は解除を完了しません。")
        }
    }
}
