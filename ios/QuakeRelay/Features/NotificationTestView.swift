import SwiftUI

struct NotificationTestView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var settings: AppSettings
    @State private var style = "normal"

    var body: some View {
        Section {
            Picker("確認する通知", selection: $style) {
                Text("通常通知").tag("normal")
                Text("警報音・Time Sensitive").tag("warning")
            }
            TimelineView(.periodic(from: .now, by: 1)) { context in
                let waiting = context.date < (environment.notificationTestAvailableAt ?? .distantPast)
                Button {
                    Task { await environment.sendNotificationTest(style: style) }
                } label: {
                    if environment.isTestingNotification { ProgressView() }
                    else { Text(waiting ? "1分に1回まで送信できます" : "この端末へテスト通知を送信") }
                }
                .disabled(waiting || environment.isTestingNotification || !settings.notificationsEnabled)
                .accessibilityIdentifier("sendNotificationTest")
            }
            if let result = environment.notificationTestResult {
                Text(result.status == "apns_accepted"
                     ? "Appleが受け付けました。実際に端末へ表示されたか確認してください。"
                     : result.status == "rejected"
                         ? "Appleが送信を受け付けませんでした。診断のAPNs登録を確認してください。"
                         : "送信結果は未確認です。同じ要求を確認しても通知は再送しません。")
                    .font(.caption)
            }
            if let error = environment.notificationTestError {
                Text(error).font(.caption).foregroundStyle(.red)
            }
        } header: { Text("通知テスト") } footer: {
            Text("保存済みの設定でこの端末だけへ送信します。地震情報ではありません。ロック画面で確認するときは、送信後すぐ画面をロックしてください。")
        }
    }
}
