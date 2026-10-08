import SwiftUI
import UIKit

struct SettingsView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var repository: EventRepository
    @EnvironmentObject private var notifications: NotificationCoordinator
    @EnvironmentObject private var deviceRegistration: DeviceRegistrationCoordinator
    @EnvironmentObject private var liveActivities: LiveActivityCoordinator

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
                if settings.eewNotificationsEnabled {
                    Toggle("予報の各報更新", isOn: $settings.eewForecastEnabled)
                    Toggle("警報", isOn: $settings.eewWarningEnabled)
                }

                Toggle("津波情報", isOn: $settings.tsunamiNotificationsEnabled)
                    .disabled(!settings.notificationsEnabled)
                if settings.tsunamiNotificationsEnabled {
                    Toggle("津波警報・注意報", isOn: $settings.tsunamiWarningEnabled)
                    Toggle("津波予報・観測情報", isOn: $settings.tsunamiObservationsEnabled)
                }
                Toggle("南海トラフ・地震関連情報", isOn: $settings.advisoryNotificationsEnabled)
                    .disabled(!settings.notificationsEnabled)
                if settings.advisoryNotificationsEnabled {
                    Toggle("南海トラフ情報", isOn: $settings.nankaiEnabled)
                    Toggle("その他の地震関連情報", isOn: $settings.seismicAdvisoryEnabled)
                }
                Toggle("Live Activity", isOn: $settings.liveActivitiesEnabled)
                    .accessibilityIdentifier("liveActivityToggle")
                    .disabled(!settings.notificationsEnabled || !liveActivities.supportsRemoteStart)
                Text(liveActivities.statusText).font(.caption).foregroundStyle(.secondary)
                Text("iOS 17.2以降で、EEW予報・津波警報や注意報の続報をロック画面に表示します。開始時の通知と通常通知は兼用し、以降の表示更新は無音です。情報が古くなると未確認と表示します。")
                    .font(.caption).foregroundStyle(.secondary)
                if let error = liveActivities.registrationError {
                    Text(error).font(.caption).foregroundStyle(.orange)
                }

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

            Section {
                TextField("例：石川県、宮崎県", text: $settings.earthquakeRegionsText)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .accessibilityLabel("地震の通知対象地域")
                TextField("例：宮崎県、石川県加賀", text: $settings.tsunamiRegionsText)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .accessibilityLabel("津波の通知対象地域")
                Picker("地震・EEW予報の最低震度", selection: $settings.minimumIntensity) {
                    Text("指定なし").tag("")
                    ForEach(["1","2","3","4","5-","5+","6-","6+","7"], id: \.self) { value in
                        Text(value.replacingOccurrences(of: "-", with: "弱").replacingOccurrences(of: "+", with: "強")).tag(value)
                    }
                }
            } header: { Text("通知の絞り込み") } footer: {
                Text("空欄は全地域です。電文に記載された都道府県・細分地域名、津波予報区名を読点で区切って入力します。地域や震度が不明なら通知します。EEW警報には最低震度を適用せず、通知済みの情報の取消・最終報・津波の解除も受け取ります。変更後は上の保存ボタンを押してください。")
            }

            NotificationTestView()

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
