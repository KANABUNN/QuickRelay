import SwiftUI

struct HistoryFilterView: View {
    @Binding var filter: HistoryFilter
    let category: String
    @Environment(\.dismiss) private var dismiss
    private var types: [RelayEventType] {
        switch category {
        case "tsunami": [.tsunamiWarning, .tsunamiInfo]
        case "advisory": [.nankaiInfo, .seismicAdvisory, .earthquakeData]
        default: [.earthquakeInfo, .earthquakeUpdate, .eewForecast, .eewWarning, .eewCancel]
        }
    }
    var body: some View {
        NavigationStack {
            Form {
                Section("表示する履歴") {
                    Toggle("未読のみ", isOn: $filter.unreadOnly).accessibilityIdentifier("unreadOnly")
                    Toggle("ピン留めのみ", isOn: $filter.pinnedOnly).accessibilityIdentifier("pinnedOnly")
                    Picker("情報種別", selection: $filter.eventType) {
                        Text("すべて").tag("")
                        ForEach(types) { Text($0.displayName).tag($0.rawValue) }
                    }
                    if category == "earthquake" {
                        Picker("最低震度", selection: $filter.minimumIntensity) {
                            Text("指定しない").tag("")
                            ForEach(IntensityValue.choices, id: \.self) { Text("震度\($0)以上").tag($0) }
                        }
                    }
                }
                Section("最新発表日") {
                    Toggle("日付を指定", isOn: $filter.dateRangeEnabled)
                    if filter.dateRangeEnabled {
                        DatePicker("開始日", selection: $filter.startDate, displayedComponents: .date)
                            .onChange(of: filter.startDate) { _, start in
                                if filter.endDate < start { filter.endDate = start }
                            }
                        DatePicker("終了日", selection: $filter.endDate, in: filter.startDate..., displayedComponents: .date)
                    }
                }
                Section {
                    Button("絞り込みを解除") { filter = HistoryFilter() }
                } footer: {
                    Text("履歴の表示だけを絞り込みます。通知の設定は変わりません。震度が不明な履歴は、最低震度を指定すると表示対象から外れます。")
                }
            }
            .navigationTitle("履歴の絞り込み").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完了") { dismiss() } } }
        }
    }
}
