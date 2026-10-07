import SwiftUI

struct PairingView: View {
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var settings: AppSettings

    @State private var pairingCode = ""
    @State private var installationID = ""
    @State private var isPairing = false

    var body: some View {
        Form {
            Section {
                Text("VPSの quakerelay pair コマンドで発行した8桁コードを入力してください。コードの有効期間は10分です。")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)

                TextField("12345678", text: $pairingCode)
                    .keyboardType(.numberPad)
                    .textContentType(.oneTimeCode)
                    .font(.title2.monospacedDigit())
                    .onChange(of: pairingCode) { _, value in
                        pairingCode = String(value.filter { "0123456789".contains($0) }.prefix(8))
                    }
                    .disabled(isPairing)

                Button {
                    isPairing = true
                    let code = pairingCode
                    Task {
                        _ = await environment.pair(code: code)
                        isPairing = false
                    }
                } label: {
                    HStack {
                        if isPairing { ProgressView() }
                        Text("ペアリング")
                    }
                    .frame(maxWidth: .infinity)
                }
                .disabled(pairingCode.count != 8 || isPairing)
            } header: {
                Text("端末ペアリング")
            }

            Section("接続先") {
                TextField("https://quake.example.jp/api/v1", text: $settings.serverBaseURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .disabled(isPairing)
                LabeledContent("Installation ID", value: installationID)
                    .font(.caption)
                    .textSelection(.enabled)
            }

            if let error = environment.pairingError ?? environment.startupError {
                Section("エラー") {
                    Text(error)
                        .foregroundStyle(.red)
                }
            }

            Section {
                Label(
                    "QuakeRelayは公式の防災情報伝達手段を代替しません。通信・DMDATA・VPS・APNsの状態により遅延や欠落が起こり得ます。",
                    systemImage: "exclamationmark.triangle"
                )
                .font(.footnote)
                .foregroundStyle(.secondary)
            }
        }
        .navigationTitle("Quick Relay")
        .task {
            installationID = (try? environment.credentials.installationID()) ?? "取得失敗"
        }
    }
}
